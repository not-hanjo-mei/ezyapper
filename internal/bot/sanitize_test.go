package bot

import "testing"

func TestSanitizeReplyDropsAnnotationAndMarkupLines(t *testing.T) {
	in := "Sure, here it is!\n" +
		"[Audio 1 (discord_1501570111138435122_1501.mp3, 7s): hello there]\n" +
		"[Audio 2 transcript (voice message, 3s): hi]\n" +
		"  attachments: [Image 1: a cat]\n" +
		"[Image 2: (could not be processed)]\n" +
		"[Image 3]\n" +
		"[Video 1 (clip.mp4): a dog]\n" +
		"[Sticker: wave — a cat waving]\n" +
		"[Embed (YouTube) \"Song\": by someone]\n" +
		"[Attached Image 1: a bike]\n" +
		"</rfl:parameter>\n" +
		"<xxx:invoke name=\"speak\">\n" +
		"<function_calls>\n" +
		"<parameter name=\"text\">hi</parameter>\n" +
		"<tool_call>\n" +
		"</tool_call>\n" +
		"Anything else?"
	got, removed := sanitizeReply(in)
	if got != "Sure, here it is!\nAnything else?" || removed != 15 {
		t.Fatalf("got %q (removed %d)", got, removed)
	}
}

// Normal prose and code with angle brackets or brackets survive.
func TestSanitizeReplyKeepsOrdinaryText(t *testing.T) {
	cases := []string{
		"Use a <div> here, and <b>bold</b> there.",
		"if a < b && b > c { return }",
		"<div class=\"x\">hello</div>",
		"fn first<T>(v: Vec<T>) -> Option<T>",
		"<@123> what do you think?",
		"<parameterized> is not a tool tag",
		"<invoker> neither",
		"[Image of a cat would be nice]",
		"[Video games are fun]",
		"[link](https://example.com)",
		"[Audio] was great",
		"I attached the image: [Image 1: a cat] see above", // not at line start
		"```xml\n<parameter name=\"x\">kept in code</parameter>\n[Image 1: kept in code]\n```",
	}
	for _, c := range cases {
		if got, removed := sanitizeReply(c); got != c || removed != 0 {
			t.Errorf("sanitizeReply(%q) = %q (removed %d), want it unchanged", c, got, removed)
		}
	}
}

func TestSanitizeReplyEmptyAfterCleanup(t *testing.T) {
	got, removed := sanitizeReply("  \n[Audio 1 (discord_1_2.mp3, 7s): hello]\n</rfl:parameter>\n")
	if got != "" || removed != 2 {
		t.Fatalf("got %q (removed %d), want nothing left", got, removed)
	}
}
