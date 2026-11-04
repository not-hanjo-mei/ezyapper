package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/types"
)

// Audio transcripts and video descriptions reach the consolidation model's
// conversation text like image descriptions do.
func TestBuildConversationText_IncludesAudioAndVideoText(t *testing.T) {
	c := &Consolidator{ownBotID: "bot"}
	msgs := []*DiscordMessage{
		{ID: "1", AuthorID: "u1", Username: "alice", Timestamp: time.Unix(0, 0), Media: []types.MediaAttachment{
			{ID: "a1", Kind: types.MediaVoice, Text: "my dog is called Pixel", Transcript: true},
		}},
		{ID: "2", AuthorID: "u1", Username: "alice", Content: "watch", Timestamp: time.Unix(0, 0), Media: []types.MediaAttachment{
			{ID: "a2", Kind: types.MediaAudio, Text: "a piano melody"},
			{ID: "v1", Kind: types.MediaVideo, Text: "a cat jumps on a sofa"},
			{ID: "v2", Kind: types.MediaVideo}, // not processed: no line
		}},
		{ID: "3", AuthorID: "u1", Username: "alice", Content: "look", Timestamp: time.Unix(0, 0),
			ImageURLs: []string{"https://cdn.discordapp.com/attachments/1/2/a.png"}, ImageDescriptions: []string{"a red bike"}},
	}
	text, images := c.buildConversationText(context.Background(), msgs, "")
	for _, want := range []string{
		"  [Attached Audio 1 transcript: my dog is called Pixel]\n",
		"  [Attached Audio 1: a piano melody]\n",
		"  [Attached Video 1: a cat jumps on a sofa]\n",
		"  [Attached Image 1: a red bike]\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("conversation is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Attached Video 2") {
		t.Errorf("an unprocessed video must not get a line:\n%s", text)
	}
	if images != 1 {
		t.Errorf("images = %d", images)
	}
}

// A voice message or a photo has no text; the entropy gate must not drop it
// when its media carries content.
func TestPassesEntropyGate_MediaOnlyMessages(t *testing.T) {
	cfg := EntropyGateConfig{MinUniqueWordRatio: 0.3}
	voice := &DiscordMessage{Media: []types.MediaAttachment{{Kind: types.MediaVoice, Text: "hello there"}}}
	if !PassesEntropyGate(voice, cfg) {
		t.Error("a transcribed voice message must pass")
	}
	unprocessed := &DiscordMessage{Media: []types.MediaAttachment{{Kind: types.MediaVoice}}}
	if PassesEntropyGate(unprocessed, cfg) {
		t.Error("a voice message without a transcript has nothing to consolidate")
	}
	photo := &DiscordMessage{ImageURLs: []string{"https://cdn.discordapp.com/attachments/1/2/a.png"}}
	if !PassesEntropyGate(photo, cfg) {
		t.Error("an image-only message must pass")
	}
}
