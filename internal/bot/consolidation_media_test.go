package bot

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ezyapper/internal/config"
	"ezyapper/internal/memory"
	"ezyapper/internal/types"
)

type capturingConsolidation struct {
	consolidationManager
	mu   sync.Mutex
	msgs []*memory.DiscordMessage
}

func (c *capturingConsolidation) ConsolidateChannel(ctx context.Context, channelID string, messages []*memory.DiscordMessage) error {
	c.mu.Lock()
	c.msgs = messages
	c.mu.Unlock()
	return nil
}

func (c *capturingConsolidation) ConsumeChannelMessageCount(channelID string, consumed int) int {
	return 0
}

// The voice message's transcript (cached by the reply path) reaches channel
// consolidation; before, consolidation only saw the empty message text.
func TestChannelConsolidation_GetsAudioTranscript(t *testing.T) {
	var asrCalls int32
	asr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&asrCalls, 1)
		_, _ = io.WriteString(w, `{"text":"I moved to Osaka last week"}`)
	}))
	defer asr.Close()
	b, files := mediaTestBot(t, config.ModalitiesConfig{Audio: config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, ModalityModel: config.ModalityModel{Model: "asr", APIBaseURL: asr.URL}}})
	cfg := b.cfg()
	cfg.Memory.Consolidation.Enabled = true
	cfg.Memory.ConsolidationInterval = 1
	cfg.Discord.ConsolidationTimeoutSec = 5
	cons := &capturingConsolidation{}
	b.consolidation = cons
	b.ctx = context.Background()
	b.channelConsolidating = map[string]bool{}
	b.channelMessageBuffer = map[string][]*types.DiscordMessage{}

	voice := voiceMsg("v1", files.URL)
	// Reply path: the current message is transcribed and cached.
	_ = b.annotateCurrent(context.Background(), *voice, GenerateContext{})
	b.addMessageToChannelBuffer("c1", voice)

	b.triggerChannelConsolidation(context.Background(), "c1", 1)
	b.wg.Wait()

	cons.mu.Lock()
	defer cons.mu.Unlock()
	if len(cons.msgs) != 1 || len(cons.msgs[0].Media) != 1 {
		t.Fatalf("consolidated messages = %+v", cons.msgs)
	}
	if got := cons.msgs[0].Media[0]; !strings.Contains(got.Text, "Osaka") || !got.Transcript {
		t.Fatalf("voice attachment = %+v, want the transcript", got)
	}
	if asrCalls != 1 {
		t.Fatalf("asr calls = %d, want the cached transcript reused", asrCalls)
	}
}
