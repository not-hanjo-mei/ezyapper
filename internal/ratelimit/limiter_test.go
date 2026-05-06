package ratelimit

import (
	"testing"
	"time"
)

func TestNewLimiter(t *testing.T) {
	limiter := NewLimiter(10, 5*time.Second, time.Minute)
	if limiter == nil {
		t.Fatal("NewLimiter returned nil")
	}
	if limiter.maxPerMinute != 10 {
		t.Errorf("expected maxPerMinute to be 10, got %d", limiter.maxPerMinute)
	}
	if limiter.cooldownPeriod != 5*time.Second {
		t.Errorf("expected cooldownPeriod to be 5s, got %v", limiter.cooldownPeriod)
	}
}

func TestLimiter_Check_Cooldown(t *testing.T) {
	limiter := NewLimiter(10, 100*time.Millisecond, time.Minute)

	// First request should pass
	if !limiter.Check("channel1", "user1") {
		t.Error("first request should be allowed")
	}

	// Set cooldown
	limiter.SetCooldown("user1", 50*time.Millisecond)

	// Request during cooldown should be blocked
	if limiter.Check("channel1", "user1") {
		t.Error("request during cooldown should be blocked")
	}

	// Wait for cooldown to expire
	time.Sleep(60 * time.Millisecond)

	// Request after cooldown should pass
	if !limiter.Check("channel1", "user1") {
		t.Error("request after cooldown should be allowed")
	}
}

func TestLimiter_Check_RateLimit(t *testing.T) {
	limiter := NewLimiter(3, 0, time.Minute) // 3 requests per minute, no cooldown

	// First 3 requests should pass
	for i := 0; i < 3; i++ {
		if !limiter.Check("channel1", "user1") {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	// 4th request should be blocked (rate limit)
	if limiter.Check("channel1", "user1") {
		t.Error("4th request should be blocked by rate limit")
	}

	// Different user should pass
	if !limiter.Check("channel1", "user2") {
		t.Error("different user should be allowed")
	}

	// Different channel should pass
	if !limiter.Check("channel2", "user1") {
		t.Error("different channel should be allowed")
	}
}

func TestLimiter_Check_Reset(t *testing.T) {
	limiter := NewLimiter(2, 0, time.Minute)

	// Use up the rate limit
	limiter.Check("channel1", "user1")
	limiter.Check("channel1", "user1")

	// Should be blocked
	if limiter.Check("channel1", "user1") {
		t.Error("should be blocked after rate limit")
	}

	// Manually reset by setting resetTime in the past
	limiter.mu.Lock()
	limiter.rateLimitCache["channel1:user1"].resetTime = time.Now().Add(-time.Second)
	limiter.mu.Unlock()

	// Should be allowed after reset
	if !limiter.Check("channel1", "user1") {
		t.Error("should be allowed after reset")
	}
}

func TestLimiter_Cleanup(t *testing.T) {
	limiter := NewLimiter(10, 0, time.Minute)

	// Add some entries
	limiter.Check("channel1", "user1")
	limiter.Check("channel2", "user2")

	// Verify entries exist
	limiter.mu.RLock()
	if len(limiter.rateLimitCache) != 2 {
		t.Errorf("expected 2 entries, got %d", len(limiter.rateLimitCache))
	}
	limiter.mu.RUnlock()

	// Manually expire entries
	limiter.mu.Lock()
	for _, info := range limiter.rateLimitCache {
		info.resetTime = time.Now().Add(-time.Second)
	}
	limiter.mu.Unlock()

	// Cleanup
	limiter.Cleanup()

	// Verify entries are removed
	limiter.mu.RLock()
	if len(limiter.rateLimitCache) != 0 {
		t.Errorf("expected 0 entries after cleanup, got %d", len(limiter.rateLimitCache))
	}
	limiter.mu.RUnlock()
}

func TestLimiter_Concurrent(t *testing.T) {
	limiter := NewLimiter(100, 0, time.Minute)

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				limiter.Check("channel1", "user1")
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have exactly 100 (limited by maxPerMinute)
	limiter.mu.RLock()
	info := limiter.rateLimitCache["channel1:user1"]
	if info != nil && info.count > 100 {
		t.Errorf("count should not exceed 100, got %d", info.count)
	}
	limiter.mu.RUnlock()
}
