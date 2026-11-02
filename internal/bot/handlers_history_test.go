package bot

import (
	"testing"
)

func TestPlanRecentImageEnrichment_EmptyNoReference(t *testing.T) {
	if planRecentImageEnrichment("", "").allows("img-msg") {
		t.Error("expected no enrichment for empty content with no reference")
	}
}

func TestPlanRecentImageEnrichment_ReplyToImageMessage(t *testing.T) {
	if !planRecentImageEnrichment("", "img-msg").allows("img-msg") {
		t.Error("expected enrichment when replying to the image message itself")
	}
}

// Regression: replying to an unrelated message used to trigger a vision call
// on whatever image was most recent in history.
func TestPlanRecentImageEnrichment_ReplyToOtherMessage(t *testing.T) {
	if planRecentImageEnrichment("ok sure", "text-msg").allows("img-msg") {
		t.Error("expected no enrichment when replying to a different message")
	}
}

func TestPlanRecentImageEnrichment_KeywordSubstringCaseInsensitive(t *testing.T) {
	if !planRecentImageEnrichment("an image link", "").allows("img-msg") {
		t.Error("expected enrichment for content containing 'image'")
	}
	if !planRecentImageEnrichment("IMGUR", "").allows("img-msg") {
		t.Error("expected enrichment for 'IMGUR' (lowercased contains 'img')")
	}
}

func TestPlanRecentImageEnrichment_WhitespaceOnly(t *testing.T) {
	if planRecentImageEnrichment("   \t  \n ", "").allows("img-msg") {
		t.Error("expected no enrichment for whitespace-only content")
	}
}

func TestPlanRecentImageEnrichment_NoKeywordNoReference(t *testing.T) {
	if planRecentImageEnrichment("plain text", "").allows("img-msg") {
		t.Error("expected no enrichment without keyword or reference")
	}
}

func TestContainsImageKeyword_Matches(t *testing.T) {
	keywords := []string{
		"an image link",
		"imgur",
		"my photo album",
		"nice picture",
		"take a pic",
		"full screenshot",
	}
	for _, c := range keywords {
		if !containsImageKeyword(c) {
			t.Errorf("containsImageKeyword(%q) = false, want true", c)
		}
	}
}

func TestContainsImageKeyword_NoMatch(t *testing.T) {
	if containsImageKeyword("plain text") {
		t.Error("expected false for content without any image keyword")
	}
}

func TestContainsImageKeyword_Empty(t *testing.T) {
	if containsImageKeyword("") {
		t.Error("expected false for empty content")
	}
}
