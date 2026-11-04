// Package media turns image, audio, video (and sticker) content of Discord
// messages into text for the base model: descriptions and transcripts added
// to the user prompt. The reply model never receives media. It is configured
// by core.ai.modalities.
package media

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

// FailureTTL is how long a permanent failure (see IsPermanent) is cached, so
// an attachment that can never be processed is not downloaded or sent to the
// model again on every turn, yet is retried after a while.
const FailureTTL = 5 * time.Minute

// Cache keeps the text media was turned into, keyed by stable IDs
// (attachment or sticker IDs, CDN paths without the signed query), never
// signed CDN URLs.
//
// Entries the prompt window still shows are pinned (see Pin): they never
// expire and are never evicted, so a media attachment is processed once
// for as long as it stays in context. Other entries expire after ttl
// without being read (every read refreshes them) and, over max entries, the
// least recently used go first. Permanent failures are kept FailureTTL.
type Cache struct {
	mu       sync.Mutex
	entries  map[string]*cacheEntry
	inflight map[string]*call
	// pins is the pinned key set of each channel; pinned counts the
	// channels pinning a key.
	pins   map[string]map[string]struct{}
	pinned map[string]int
	ttl    time.Duration
	max    int
	now    func() time.Time
}

// call is one in-flight computation of a key (see Do).
type call struct {
	done  chan struct{}
	value string
	err   error
}

type cacheEntry struct {
	value string
	// failure is the message of a cached permanent failure ("" for a
	// result); failedAt is when it happened.
	failure  string
	failedAt time.Time
	// used is the last time the entry was stored or read.
	used time.Time
}

// NewCache creates a cache. ttl <= 0 disables idle expiry; max <= 0
// disables the size bound.
func NewCache(ttl time.Duration, max int) *Cache {
	return &Cache{
		entries:  map[string]*cacheEntry{},
		inflight: map[string]*call{},
		pins:     map[string]map[string]struct{}{},
		pinned:   map[string]int{},
		ttl:      ttl,
		max:      max,
		now:      time.Now,
	}
}

// SetLimits updates the idle TTL and size bound (config hot reload).
func (c *Cache) SetLimits(ttl time.Duration, max int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.ttl, c.max = ttl, max
	c.pruneLocked()
	c.mu.Unlock()
}

// Pin sets the keys of the media the prompt window of channelID shows,
// replacing the channel's previous set (an empty keys unpins the channel).
// Pinned entries never expire and are never evicted. Keys that leave the
// window go back to the idle TTL, counted from this call.
func (c *Cache) Pin(channelID string, keys []string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k := range c.pins[channelID] {
		if c.pinned[k]--; c.pinned[k] <= 0 {
			delete(c.pinned, k)
		}
		if e, ok := c.entries[k]; ok {
			e.used = now
		}
	}
	delete(c.pins, channelID)
	if len(keys) > 0 {
		set := make(map[string]struct{}, len(keys))
		for _, k := range keys {
			if k == "" {
				continue
			}
			if _, dup := set[k]; dup {
				continue
			}
			set[k] = struct{}{}
			c.pinned[k]++
		}
		c.pins[channelID] = set
	}
	c.pruneLocked()
}

// Pinned reports whether key is pinned by any channel.
func (c *Cache) Pinned(key string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pinned[key] > 0
}

// lookupLocked returns the live entry of key, dropping it when it expired.
func (c *Cache) lookupLocked(key string, now time.Time) *cacheEntry {
	e, ok := c.entries[key]
	if !ok {
		return nil
	}
	if c.expiredLocked(key, e, now) {
		delete(c.entries, key)
		return nil
	}
	return e
}

func (c *Cache) expiredLocked(key string, e *cacheEntry, now time.Time) bool {
	if e.failure != "" {
		return now.Sub(e.failedAt) > FailureTTL
	}
	if c.pinned[key] > 0 {
		return false
	}
	return c.ttl > 0 && now.Sub(e.used) > c.ttl
}

// Get returns a cached result (never a cached failure) and refreshes it.
func (c *Cache) Get(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	e := c.lookupLocked(key, now)
	if e == nil || e.failure != "" {
		return "", false
	}
	e.used = now
	return e.value, true
}

// cachedFailure is the error Do returns for a cached permanent failure.
type cachedFailure struct{ msg string }

func (e *cachedFailure) Error() string { return e.msg + " (cached failure)" }

// Do returns the cached value of key, or computes it with fn and caches a
// successful result, or a permanent failure (IsPermanent) for FailureTTL.
// Timeouts, rate limits, server errors, cancellation and empty output are
// never cached. Concurrent calls for the same key share one fn call: the
// reply path and memory consolidation can ask for the same attachment at
// the same moment, and it must still be processed once. A nil cache just
// calls fn.
func (c *Cache) Do(key string, fn func() (string, error)) (string, error) {
	if c == nil || key == "" {
		return fn()
	}
	c.mu.Lock()
	now := c.now()
	if e := c.lookupLocked(key, now); e != nil {
		if e.failure != "" {
			c.mu.Unlock()
			return "", &cachedFailure{msg: e.failure}
		}
		e.used = now
		v := e.value
		c.mu.Unlock()
		return v, nil
	}
	if cl, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-cl.done
		return cl.value, cl.err
	}
	cl := &call{done: make(chan struct{})}
	c.inflight[key] = cl
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.inflight, key)
		c.mu.Unlock()
		close(cl.done)
	}()
	cl.value, cl.err = fn()
	switch {
	case cl.err == nil && strings.TrimSpace(cl.value) != "":
		c.Set(key, cl.value)
	case cl.err != nil && IsPermanent(cl.err):
		c.setFailure(key, cl.err)
	}
	return cl.value, cl.err
}

// Set stores value under key.
func (c *Cache) Set(key, value string) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	c.entries[key] = &cacheEntry{value: value, used: c.now()}
	c.pruneLocked()
	c.mu.Unlock()
}

func (c *Cache) setFailure(key string, err error) {
	msg := err.Error()
	var cf *cachedFailure
	if errors.As(err, &cf) {
		msg = cf.msg
	}
	c.mu.Lock()
	now := c.now()
	c.entries[key] = &cacheEntry{failure: msg, failedAt: now, used: now}
	c.pruneLocked()
	c.mu.Unlock()
}

// DeletePrefix drops every entry whose key starts with one of prefixes
// (a media kind's results after its model or prompt changed). Pins stay:
// the keys are recomputed on the next turn that needs them.
func (c *Cache) DeletePrefix(prefixes ...string) int {
	if c == nil || len(prefixes) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for k := range c.entries {
		for _, p := range prefixes {
			if p != "" && strings.HasPrefix(k, p) {
				delete(c.entries, k)
				n++
				break
			}
		}
	}
	return n
}

// Len returns the number of entries (expired ones included until pruned).
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Prune drops expired entries and, over the size bound, the least recently
// used unpinned ones.
func (c *Cache) Prune() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.pruneLocked()
	c.mu.Unlock()
}

func (c *Cache) pruneLocked() {
	now := c.now()
	for k, e := range c.entries {
		if c.expiredLocked(k, e, now) {
			delete(c.entries, k)
		}
	}
	if c.max <= 0 || len(c.entries) <= c.max {
		return
	}
	type kv struct {
		k    string
		used time.Time
	}
	evictable := make([]kv, 0, len(c.entries))
	for k, e := range c.entries {
		if c.pinned[k] > 0 && e.failure == "" {
			continue
		}
		evictable = append(evictable, kv{k, e.used})
	}
	slices.SortFunc(evictable, func(a, b kv) int { return a.used.Compare(b.used) })
	excess := len(c.entries) - c.max
	for i := 0; i < excess && i < len(evictable); i++ {
		delete(c.entries, evictable[i].k)
	}
}
