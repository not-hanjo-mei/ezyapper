package types

import "testing"

func resolvedMsg() *DiscordMessage {
	return &DiscordMessage{Content: "hi", ResolvedMedia: []ResolvedMedia{
		{Kind: ResolvedImage, Text: "a red\nbike", Status: MediaOK},
		{Kind: ResolvedImage, Status: MediaFailed},
		{Kind: ResolvedImage, Status: MediaPending},
		{Kind: ResolvedSticker, Label: "wave", Text: "a waving cat", Status: MediaOK},
		{Kind: ResolvedSticker, Label: "plain", Status: MediaOK},
		{Kind: ResolvedVoice, Label: "voice message, 7s", Text: "meet at noon", TextKind: TextTranscript, Status: MediaOK},
		{Kind: ResolvedAudio, Label: "song.mp3", Status: MediaPending},
		{Kind: ResolvedAudio, Label: "x.mp3", Status: MediaFailed, TextKind: TextTranscript},
		{Kind: ResolvedVideo, Label: "clip.mp4", Text: "a dog runs", TextKind: TextDescription, Status: MediaOK},
	}}
}

func TestRenderAttachmentsPrompt(t *testing.T) {
	got := RenderAttachments(resolvedMsg(), StylePrompt)
	want := "  attachments: [Image 1: a red bike] [Image 2: (could not be processed)] [Image 3]" +
		" [Sticker: wave — a waving cat] [Sticker: plain]" +
		" [Audio 1 transcript (voice message, 7s): meet at noon] [Audio 2: song.mp3]" +
		" [Audio 3 (x.mp3): (could not be processed)] [Video 1 (clip.mp4): a dog runs]"
	if got != want {
		t.Fatalf("prompt style:\n got %q\nwant %q", got, want)
	}
}

func TestRenderAttachmentsMemory(t *testing.T) {
	got := RenderAttachments(resolvedMsg(), StyleMemory)
	want := "  [Attached Image 1: a red bike]\n" +
		"  [Attached Image 2: (could not be processed)]\n" +
		"  [Attached Sticker: wave — a waving cat]\n" +
		"  [Attached Sticker: plain]\n" +
		"  [Attached Audio 1 transcript: meet at noon]\n" +
		"  [Attached Audio 3: (could not be processed)]\n" +
		"  [Attached Video 1: a dog runs]"
	if got != want {
		t.Fatalf("memory style:\n got %q\nwant %q", got, want)
	}
}

func TestRenderAttachmentsQuery(t *testing.T) {
	got := RenderAttachments(resolvedMsg(), StyleQuery)
	want := "a red bike\na waving cat\nmeet at noon\na dog runs"
	if got != want {
		t.Fatalf("query style:\n got %q\nwant %q", got, want)
	}
}

func TestRenderAttachmentsEmpty(t *testing.T) {
	for _, st := range []AttachmentStyle{StylePrompt, StyleMemory, StyleQuery} {
		if got := RenderAttachments(&DiscordMessage{Content: "x"}, st); got != "" {
			t.Fatalf("style %d: no media must render nothing, got %q", st, got)
		}
		if got := RenderAttachments(nil, st); got != "" {
			t.Fatalf("nil message must render nothing, got %q", got)
		}
	}
}
