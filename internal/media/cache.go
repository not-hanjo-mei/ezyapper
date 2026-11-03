// Package media turns image, audio, video (and sticker) content of Discord
// messages into text for the base model: descriptions and transcripts added
// to the user prompt. The reply model never receives media. It is configured
// by core.ai.modalities.
package media

import (
	"slices"
	"sync"
	"time"
)

// Cache is a small TTL + size bounded string cache. Keys are stable IDs
// (attachment or sticker IDs), never signed CDN URLs.
type Cache struct {
	mu       sync.Mutex
	entries  map[string]cacheEntry
	inflight map[string]*call
	ttl      time.Duration
	max      int
	now      func() time.Time
}

// call is one in-flight computation of a key (see Do).
type call struct {
	done  chan struct{}
	value string
	err   error
}

type cacheEntry struct {
	value string
	at    time.Time
}

// NewCache creates a cache. ttl <= 0 disables expiry; max <= 0 disables the
// size bound.
func NewCache(ttl time.Duration, max int) *Cache {
	return &Cache{entries: map[string]cacheEntry{}, inflight: map[string]*call{}, ttl: ttl, max: max, now: time.Now}
}

// SetLimits updates the TTL and size bound (config hot reload).
func (c *Cache) SetLimits(ttl time.Duration, max int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.ttl, c.max = ttl, max
	c.pruneLocked()
	c.mu.Unlock()
}

// Get returns a live entry.
func (c *Cache) Get(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if c.ttl > 0 && c.now().Sub(e.at) > c.ttl {
		delete(c.entries, key)
		return "", false
	}
	return e.value, true
}

// Do returns the cached value of key, or computes it with fn and caches a
// successful result. Concurrent calls for the same key share one fn call:
// the reply path and memory consolidation can ask for the same attachment at
// the same moment, and it must still be processed once. A nil cache just
// calls fn.
func (c *Cache) Do(key string, fn func() (string, error)) (string, error) {
	if c == nil || key == "" {
		return fn()
	}
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	c.mu.Lock()
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
	if cl.err == nil {
		c.Set(key, cl.value)
	}
	return cl.value, cl.err
}

// Set stores value under key.
func (c *Cache) Set(key, value string) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	c.entries[key] = cacheEntry{value: value, at: c.now()}
	c.pruneLocked()
	c.mu.Unlock()
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

// Prune drops expired entries and, over the size bound, the oldest ones.
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
	if c.ttl > 0 {
		for k, e := range c.entries {
			if now.Sub(e.at) > c.ttl {
				delete(c.entries, k)
			}
		}
	}
	if c.max <= 0 || len(c.entries) <= c.max {
		return
	}
	type kv struct {
		k  string
		at time.Time
	}
	all := make([]kv, 0, len(c.entries))
	for k, e := range c.entries {
		all = append(all, kv{k, e.at})
	}
	slices.SortFunc(all, func(a, b kv) int { return a.at.Compare(b.at) })
	for _, e := range all[:len(all)-c.max] {
		delete(c.entries, e.k)
	}
}
