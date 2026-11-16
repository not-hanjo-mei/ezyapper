package bot

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"ezyapper/internal/config"
	"ezyapper/internal/memory"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

type queryRecordingStore struct {
	memory.MemoryStore
	mu      sync.Mutex
	queries []string
}

func (s *queryRecordingStore) Search(ctx context.Context, userID, query string, opts *memory.SearchOptions) ([]*memory.Record, error) {
	s.mu.Lock()
	s.queries = append(s.queries, query)
	s.mu.Unlock()
	return nil, nil
}

func voiceCreate(base string) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "m1", ChannelID: "c1", GuildID: "g1",
		Author: &discordgo.User{ID: "u1", Username: "alice"},
		Flags:  discordgo.MessageFlagsIsVoiceMessage,
		Attachments: []*discordgo.MessageAttachment{{
			ID: "att-voice", URL: base + "/voice.ogg", Filename: "voice-message.ogg", ContentType: "audio/ogg", DurationSecs: 2,
		}},
	}}
}

// A voice message has no content. The memory search must embed its
// transcript, not an empty string (emrank answered 400 for "").
func TestSearchMemories_VoiceMessageUsesTranscript(t *testing.T) {
	var asrCalls int32
	asr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&asrCalls, 1)
		_, _ = io.WriteString(w, `{"text":"my cat is called Mochi"}`)
	}))
	defer asr.Close()
	b, files := mediaTestBot(t, config.ModalitiesConfig{Audio: config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, ModalityModel: config.ModalityModel{Model: "asr", APIBaseURL: asr.URL}}})
	b.cfg().Memory.Retrieval.TopK = 3
	store := &queryRecordingStore{}
	b.memoryStore = store

	m := voiceCreate(files.URL)
	current := b.resolveCurrent(context.Background(), m)
	b.searchMemories(context.Background(), m, nil, types.RenderAttachments(current, types.StyleQuery))

	if len(store.queries) != 1 || store.queries[0] != "my cat is called Mochi" {
		t.Fatalf("search queries = %q, want the transcript", store.queries)
	}
	// The reply prompt uses the same resolved message: no second ASR call.
	mc, gc := b.buildModeContexts(m, nil, "alice", "g", current)
	if gc.Current != current || mc.MessageID != "m1" || asrCalls != 1 {
		t.Fatalf("asrCalls=%d, want the current message resolved once", asrCalls)
	}
}

// With no text at all (media disabled), the user memory search is skipped
// instead of embedding "".
func TestSearchMemories_EmptyQuerySkipped(t *testing.T) {
	b, files := mediaTestBot(t, config.ModalitiesConfig{})
	b.cfg().Memory.Retrieval.TopK = 3
	store := &queryRecordingStore{}
	b.memoryStore = store

	m := voiceCreate(files.URL)
	b.searchMemories(context.Background(), m, nil, types.RenderAttachments(b.resolveCurrent(context.Background(), m), types.StyleQuery))
	if len(store.queries) != 0 {
		t.Fatalf("search called with %q, want no search", store.queries)
	}
}

func TestMemorySearchQuery(t *testing.T) {
	if q := memorySearchQuery("", "", nil, "bot"); q != "" {
		t.Fatalf("empty message: %q", q)
	}
	if q := memorySearchQuery("", "hello", nil, "bot"); q != "hello" {
		t.Fatalf("voice only: %q", q)
	}
	if q := memorySearchQuery("look", "a cat", nil, "bot"); q != "look\na cat" {
		t.Fatalf("text and image: %q", q)
	}
}
