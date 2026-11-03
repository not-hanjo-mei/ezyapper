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
	b.searchMemories(context.Background(), m, nil, b.currentMediaText(context.Background(), m, nil))

	if len(store.queries) != 1 || !strings.Contains(store.queries[0], "my cat is called Mochi") {
		t.Fatalf("search queries = %q, want the transcript", store.queries)
	}
	// The reply prompt reuses the cached transcript.
	cur := *voiceMsg("x", files.URL)
	cur.Media[0].ID = "att-voice"
	if got := b.annotateCurrent(context.Background(), cur, GenerateContext{}); !strings.Contains(got, "Mochi") || asrCalls != 1 {
		t.Fatalf("annotateCurrent=%q asrCalls=%d, want the cached transcript and one ASR call", got, asrCalls)
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
	b.searchMemories(context.Background(), m, nil, b.currentMediaText(context.Background(), m, nil))
	if len(store.queries) != 0 {
		t.Fatalf("search called with %q, want no search", store.queries)
	}
}

func TestMemorySearchQuery(t *testing.T) {
	if q := memorySearchQuery("", "", nil, "bot"); q != "" {
		t.Fatalf("empty message: %q", q)
	}
	if q := memorySearchQuery("", "\n[Audio 1 transcript (voice message, 2s): hello]", nil, "bot"); q != "[Audio 1 transcript (voice message, 2s): hello]" {
		t.Fatalf("voice only: %q", q)
	}
	if q := memorySearchQuery("look", "\n[Image 1: a cat]", nil, "bot"); q != "look\n[Image 1: a cat]" {
		t.Fatalf("text and image: %q", q)
	}
}
