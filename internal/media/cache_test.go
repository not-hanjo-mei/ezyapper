package media

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/types"
)

// fakeClock returns a cache whose clock the test moves.
func fakeClock(ttl time.Duration, max int) (*Cache, *time.Time) {
	c := NewCache(ttl, max)
	now := time.Unix(1_000_000, 0)
	c.now = func() time.Time { return now }
	return c, &now
}

// Root cause regression: entries expired a fixed time after creation, so an
// image still in the prompt window was described again after an hour.
// Pinned entries never expire and are never evicted.
func TestCachePinnedNeverExpiresOrEvicted(t *testing.T) {
	c, now := fakeClock(time.Minute, 2)
	c.Set("img", "a cat")
	c.Pin("chan", []string{"img"})
	*now = now.Add(24 * time.Hour)
	for i := range 5 {
		c.Set(fmt.Sprint("other", i), "x")
		*now = now.Add(time.Second)
	}
	c.Prune()
	if v, ok := c.Get("img"); !ok || v != "a cat" {
		t.Fatal("a pinned entry must survive the TTL and the size bound")
	}
	if c.Len() != 2 {
		t.Fatalf("len = %d, want the bound (2): pinned + newest", c.Len())
	}
}

// A channel's pins are replaced by its next prompt; an entry that leaves
// the window gets the idle TTL counted from then. Another channel pinning
// the same key keeps it.
func TestCachePinReplacesChannelSet(t *testing.T) {
	c, now := fakeClock(time.Minute, 0)
	c.Set("a", "1")
	c.Set("b", "2")
	c.Pin("c1", []string{"a", "b"})
	c.Pin("c2", []string{"b"})
	*now = now.Add(time.Hour)
	c.Pin("c1", []string{"new"}) // a and b leave c1's window
	if !c.Pinned("b") || c.Pinned("a") || !c.Pinned("new") {
		t.Fatal("pin sets: b is still pinned by c2, a by no one")
	}
	*now = now.Add(30 * time.Second)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("an entry that just left the window gets a full idle TTL")
	}
	*now = now.Add(2 * time.Minute)
	if _, ok := c.Get("a"); ok {
		t.Fatal("an unpinned idle entry must expire")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("b is still pinned by c2")
	}
	c.Pin("c2", nil)
	if c.Pinned("b") {
		t.Fatal("an empty set unpins the channel")
	}
}

// Unpinned entries expire after ttl without reads; every read refreshes.
func TestCacheIdleTTLRefreshedOnRead(t *testing.T) {
	c, now := fakeClock(time.Minute, 0)
	c.Set("k", "v")
	for range 5 {
		*now = now.Add(50 * time.Second)
		if _, ok := c.Get("k"); !ok {
			t.Fatal("a read within the idle TTL must keep the entry")
		}
	}
	if _, err := c.Do("k", func() (string, error) { t.Fatal("Do must hit"); return "", nil }); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(61 * time.Second)
	if _, ok := c.Get("k"); ok {
		t.Fatal("idle entry must expire")
	}
}

// Over the bound, the least recently used unpinned entry goes, not the
// oldest created.
func TestCacheLRUEviction(t *testing.T) {
	c, now := fakeClock(0, 2)
	c.Set("old", "1")
	*now = now.Add(time.Second)
	c.Set("mid", "2")
	*now = now.Add(time.Second)
	c.Get("old") // old is now the most recently used
	*now = now.Add(time.Second)
	c.Set("new", "3")
	if _, ok := c.Get("mid"); ok {
		t.Fatal("the least recently used entry must be evicted")
	}
	if _, ok := c.Get("old"); !ok {
		t.Fatal("a recently read entry must survive")
	}
}

func TestIsPermanent(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&permanentError{err: errors.New("over the size limit (9 > 1 bytes)")}, true},
		{fmt.Errorf("failed to fetch image: %w", errors.New(`unexpected content type "text/html" (want image/*)`)), true},
		{errors.New("media too large: 9 bytes (limit 1)"), true},
		{errors.New("download media: status 404"), true},
		{errors.New("download media: status 403"), true},
		{errors.New("download media: status 408"), false},
		{errors.New("download media: status 429"), false},
		{errors.New("download media: status 502"), false},
		{errors.New(`HTTP 404 Not Found, {"message": "Unknown Sticker"}`), true},
		{errors.New("HTTP 429 Too Many Requests, "), false},
		{errors.New("media describe: status code 400: bad image"), true},
		{errors.New("media describe: status code 404: no such model"), true},
		{errors.New("media describe: status code 413: too big"), true},
		{errors.New("transcription: status code 415: unsupported"), true},
		{errors.New("media describe: status code 422: bad"), true},
		{errors.New("media describe: status code 401: key"), false},
		{errors.New("media describe: status code 429: slow down"), false},
		{errors.New("media describe: status code 500: oops"), false},
		{errors.New("media describe: status code 503: busy"), false},
		{errors.New("media describe request failed: context deadline exceeded (Client.Timeout exceeded)"), false},
		{fmt.Errorf("x: %w", context.Canceled), false},
		{&permanentError{err: context.DeadlineExceeded}, false},
		{errors.New("model returned empty content"), false},
		{errors.New("model returned no content (finish_reason=length); raise max_tokens"), false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := IsPermanent(tc.err); got != tc.want {
			t.Errorf("IsPermanent(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
	// A transcode error is permanent unless the context ended.
	if !IsPermanent(permanent(context.Background(), errors.New("ffmpeg: exit status 1"))) {
		t.Error("a transcode error must be permanent")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if IsPermanent(permanent(ctx, errors.New("ffmpeg: signal: killed"))) {
		t.Error("a transcode killed by cancellation must not be permanent")
	}
}

// Permanent failures are cached for FailureTTL, then retried; transient ones
// and empty output are never cached.
func TestCacheDoFailureCaching(t *testing.T) {
	c, now := fakeClock(time.Hour, 10)
	var calls int
	perm := func() (string, error) { calls++; return "", errors.New("download media: status 404") }
	for range 3 {
		if _, err := c.Do("p", perm); err == nil {
			t.Fatal("want the error")
		}
	}
	if calls != 1 {
		t.Fatalf("a permanent failure must be cached, calls=%d", calls)
	}
	if _, ok := c.Get("p"); ok {
		t.Fatal("Get must not return a cached failure as a result")
	}
	*now = now.Add(FailureTTL + time.Second)
	_, _ = c.Do("p", perm)
	if calls != 2 {
		t.Fatalf("a cached failure must be retried after FailureTTL, calls=%d", calls)
	}

	// A pinned key's failure still expires.
	c.Pin("ch", []string{"p"})
	*now = now.Add(FailureTTL + time.Second)
	_, _ = c.Do("p", perm)
	if calls != 3 {
		t.Fatalf("a pinned failure must expire too, calls=%d", calls)
	}

	for name, fn := range map[string]func() (string, error){
		"timeout":   func() (string, error) { return "", errors.New("request failed: context deadline exceeded") },
		"429":       func() (string, error) { return "", errors.New("media describe: status code 429: slow") },
		"5xx":       func() (string, error) { return "", errors.New("download media: status 503") },
		"cancelled": func() (string, error) { return "", fmt.Errorf("x: %w", context.Canceled) },
		"empty":     func() (string, error) { return "", nil },
		"blank":     func() (string, error) { return "  ", nil },
	} {
		n := 0
		wrapped := func() (string, error) { n++; return fn() }
		_, _ = c.Do(name, wrapped)
		_, _ = c.Do(name, wrapped)
		if n != 2 {
			t.Errorf("%s must not be cached, calls=%d", name, n)
		}
	}
}

// Processor level: a model 415 is cached, so the same image is not sent
// again on the next turn; a 503 is retried.
func TestDescribeImagesCachesPermanentModelFailure(t *testing.T) {
	var calls int32
	status := http.StatusUnsupportedMediaType
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "nope", status)
	}))
	defer srv.Close()
	cfg := baseConfig(srv.URL)
	cfg.AI.Modalities.Image = config.ImageModalityConfig{Enabled: true, Transport: config.TransportURL}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, NewCache(time.Hour, 10))
	url := []string{"https://cdn.discordapp.com/attachments/1/2/a.png?ex=1"}
	for range 3 {
		if _, err := p.DescribeImages(context.Background(), url); err == nil {
			t.Fatal("want the error")
		}
	}
	if calls != 1 {
		t.Fatalf("415 must be cached, model calls=%d", calls)
	}
	status = http.StatusServiceUnavailable
	url = []string{"https://cdn.discordapp.com/attachments/1/3/b.png"}
	_, _ = p.DescribeImages(context.Background(), url)
	before := atomic.LoadInt32(&calls)
	_, _ = p.DescribeImages(context.Background(), url)
	if atomic.LoadInt32(&calls) == before {
		t.Fatal("503 must not be cached")
	}
}

// A voice message over the size limit, and one whose download is a 404, are
// not fetched or sent again while the failure is cached.
func TestTranscribeCachesSizeAndDownloadFailures(t *testing.T) {
	files := cdn(t)
	var asrCalls int32
	asr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&asrCalls, 1)
		_, _ = w.Write([]byte(`{"text":"hi"}`))
	}))
	defer asr.Close()
	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, MaxBytes: 5, ModalityModel: config.ModalityModel{Model: "asr", APIBaseURL: asr.URL}}
	cfg.AI.Modalities.ApplyDefaults()
	cache := NewCache(time.Hour, 10)
	p := New(cfg, cache)

	big := voice(files.URL)
	big.Size = 100
	for range 2 {
		if _, err := p.Transcribe(context.Background(), big); err == nil || !IsPermanent(err) {
			t.Fatalf("want a permanent size error, got %v", err)
		}
	}
	missing := types.MediaAttachment{ID: "gone", Kind: types.MediaVoice, URL: files.URL + "/gone.bin", ContentType: "audio/ogg", Size: 1}
	for range 2 {
		if _, err := p.Transcribe(context.Background(), missing); err == nil {
			t.Fatal("want the 404")
		}
	}
	if !errorsCached(cache, keyAudio+big.ID, keyAudio+"gone") {
		t.Fatal("both failures must be cached")
	}
	if asrCalls != 0 {
		t.Fatalf("asr calls = %d", asrCalls)
	}
}

// A transcode error (here: the ffmpeg binary does not exist) is cached.
func TestTranscodeErrorIsCached(t *testing.T) {
	files := cdn(t)
	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, Transcode: "mp3", ModalityModel: config.ModalityModel{Model: "asr", APIBaseURL: "http://unused.invalid"}}
	cfg.AI.Modalities.FFmpegPath = "/nonexistent/ffmpeg"
	cfg.AI.Modalities.ApplyDefaults()
	cache := NewCache(time.Hour, 10)
	p := New(cfg, cache)
	_, err := p.Transcribe(context.Background(), voice(files.URL))
	if err == nil || !IsPermanent(err) {
		t.Fatalf("want a permanent transcode error, got %v", err)
	}
	if !errorsCached(cache, keyAudio+"att-voice") {
		t.Fatal("the transcode error must be cached")
	}
}

func errorsCached(c *Cache, keys ...string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range keys {
		e, ok := c.entries[k]
		if !ok || e.failure == "" {
			return false
		}
	}
	return true
}

// Sticker vision goes through Do: a 404 image is cached as a failure.
func TestDescribeStickerCachesPermanentFailure(t *testing.T) {
	var fetches int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetches, 1)
		http.NotFound(w, r)
	}))
	defer host.Close()
	old := StickerURLBase
	StickerURLBase = host.URL + "/stickers/"
	defer func() { StickerURLBase = old }()
	var calls int32
	chat := chatServer(t, "never", &calls, nil)
	p, _ := stickerProcessor(t, true, chat.URL)
	s := types.StickerRef{ID: "123", Name: "Amika Thumbs Up", FormatType: types.StickerPNG}
	for range 3 {
		_ = p.AnnotateStickers(context.Background(), stickerMsg(s), true)
	}
	if fetches != 1 || calls != 0 {
		t.Fatalf("fetches=%d model calls=%d, want 1 and 0", fetches, calls)
	}
}

// Every media key a message carries is a window key, under the same keys
// the processing paths use.
func TestWindowKeys(t *testing.T) {
	msg := &types.DiscordMessage{
		ImageURLs: []string{"https://cdn.discordapp.com/attachments/1/2/a.png?ex=1&hm=2"},
		Media:     []types.MediaAttachment{{ID: "au", Kind: types.MediaAudio}, {ID: "vo", Kind: types.MediaVoice}, {ID: "vi", Kind: types.MediaVideo}},
		Stickers:  []types.StickerRef{{ID: "st"}},
	}
	got := WindowKeys(nil, msg)
	want := []string{
		"image:discord-cdn:/attachments/1/2/a.png",
		"audio:au", "audio-describe:au", "audio:vo", "audio-describe:vo", "video:vi",
		"sticker:vision:st", "sticker:info:st",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("keys = %v\nwant %v", got, want)
	}
}

// Only the kind whose model, endpoint, prompt or transcode settings change
// gets a new fingerprint.
func TestFingerprintsChangedKinds(t *testing.T) {
	base := baseConfig("http://a")
	base.AI.Modalities.ApplyDefaults()
	fp := Fingerprints(base)
	if ChangedKinds(nil, fp) != nil {
		t.Fatal("first load changes nothing")
	}
	mut := func(f func(c *config.Config)) []string {
		c := *base
		f(&c)
		return ChangedKinds(fp, Fingerprints(&c))
	}
	cases := map[string]struct {
		f    func(c *config.Config)
		want string
	}{
		"max items":       {func(c *config.Config) { c.AI.Modalities.Image.MaxItems = 9 }, "[]"},
		"image prompt":    {func(c *config.Config) { c.AI.Modalities.Image.Prompt = "x" }, "[image]"},
		"base endpoint":   {func(c *config.Config) { c.AI.APIBaseURL = "http://b" }, "[image audio video sticker]"},
		"audio transcode": {func(c *config.Config) { c.AI.Modalities.Audio.Transcode = "wav" }, "[audio]"},
		"video height":    {func(c *config.Config) { c.AI.Modalities.Video.MaxHeight = 360 }, "[video]"},
		"video model":     {func(c *config.Config) { c.AI.Modalities.Video.Model = "v2" }, "[video]"},
		"sticker prompt":  {func(c *config.Config) { c.AI.Modalities.Sticker.Prompt = "y" }, "[sticker]"},
	}
	for name, tc := range cases {
		if got := fmt.Sprint(mut(tc.f)); got != tc.want {
			t.Errorf("%s: changed = %s, want %s", name, got, tc.want)
		}
	}
}

func TestDeletePrefix(t *testing.T) {
	c := NewCache(0, 0)
	for _, k := range []string{"audio:1", "audio-describe:1", "video:1", "image:x"} {
		c.Set(k, "v")
	}
	if n := c.DeletePrefix(KindPrefixes(KindAudio)...); n != 2 || c.Len() != 2 {
		t.Fatalf("deleted %d, len %d", n, c.Len())
	}
}
