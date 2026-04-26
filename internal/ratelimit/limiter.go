// Package ratelimit provides thread-safe per-user rate limiting with cooldown support.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter provides thread-safe per-user rate limiting and cooldowns.
type Limiter struct {
	rateLimitCache map[string]*rateLimitInfo
	cooldownCache  map[string]time.Time
	maxPerMinute   int
	cooldownPeriod time.Duration
	mu             sync.RWMutex
}

type rateLimitInfo struct {
	count     int
	resetTime time.Time
}

// NewLimiter creates a new rate limiter with the specified max requests per minute and cooldown duration.
func NewLimiter(maxPerMinute int, cooldownPeriod time.Duration) *Limiter {
	return &Limiter{
		rateLimitCache: make(map[string]*rateLimitInfo),
		cooldownCache:  make(map[string]time.Time),
		maxPerMinute:   maxPerMinute,
		cooldownPeriod: cooldownPeriod,
	}
}

// Check tests whether the given user in the channel is allowed to send another message.
// Returns false if rate-limited or in cooldown.
func (l *Limiter) Check(channelID, userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	key := channelID + ":" + userID

	if cooldown, exists := l.cooldownCache[userID]; exists && now.Before(cooldown) {
		return false
	}

	if info, exists := l.rateLimitCache[key]; exists {
		if now.After(info.resetTime) {
			info.count = 0
			info.resetTime = now.Add(time.Minute)
		}

		if info.count >= l.maxPerMinute {
			return false
		}

		info.count++
	} else {
		l.rateLimitCache[key] = &rateLimitInfo{
			count:     1,
			resetTime: now.Add(time.Minute),
		}
	}

	return true
}

// SetCooldown puts a user on cooldown for the specified duration.
func (l *Limiter) SetCooldown(userID string, duration time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cooldownCache[userID] = time.Now().Add(duration)
}

// SetCooldownDefault puts a user on cooldown using the configured default cooldown period.
func (l *Limiter) SetCooldownDefault(userID string) {
	l.SetCooldown(userID, l.cooldownPeriod)
}

// Cleanup removes expired rate limit entries and cooldowns.
func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	for key, info := range l.rateLimitCache {
		if now.After(info.resetTime) {
			delete(l.rateLimitCache, key)
		}
	}

	for userID, cooldown := range l.cooldownCache {
		if now.After(cooldown) {
			delete(l.cooldownCache, userID)
		}
	}
}

// UpdateConfig hot-updates the rate limit settings without restarting.
func (l *Limiter) UpdateConfig(maxPerMinute int, cooldownPeriod time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maxPerMinute = maxPerMinute
	l.cooldownPeriod = cooldownPeriod
}
