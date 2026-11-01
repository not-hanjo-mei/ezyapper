package bot

import (
	"sync/atomic"
	"testing"

	"ezyapper/internal/config"
)

// Regression for hybrid mode calling the vision model on nearly every reply:
// Discord re-signs CDN URLs on each fetch, so a cache keyed by raw URLs never hit.
func TestHistoricalImageCacheSurvivesURLResigning(t *testing.T) {
	cfg := &config.Config{}
	cfg.Discord.ImageCacheTTLMin = 60
	cfg.Discord.ImageCacheMaxEntries = 10
	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)
	b := &Bot{configStore: cfgStore, historicalImageDescCache: make(map[string]historicalImageDescCacheEntry)}

	first := []string{"https://cdn.discordapp.com/attachments/1/2/cat.png?ex=1&is=2&hm=3&"}
	resigned := []string{"https://cdn.discordapp.com/attachments/1/2/cat.png?ex=9&is=8&hm=7&"}

	b.setHistoricalImageDescriptions("msg1", first, []string{"a cat"})
	got, ok := b.getHistoricalImageDescriptions("msg1", resigned)
	if !ok || len(got) != 1 || got[0] != "a cat" {
		t.Fatalf("expected cache hit after re-signing, got %v ok=%v", got, ok)
	}

	if _, ok := b.getHistoricalImageDescriptions("msg1", []string{"https://cdn.discordapp.com/attachments/1/2/dog.png"}); ok {
		t.Fatal("a different image must not hit the cache")
	}
}
