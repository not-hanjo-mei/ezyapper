package decision

import (
	"context"
	"os"
	"testing"
	"time"

	"ezyapper/internal/config"
)

// TestSystemOneLive calls the real System One API. It only runs with
// EZYAPPER_LIVE_JEV=1. EZYAPPER_JEV_API_KEY and EZYAPPER_JEV_BASE_URL
// override the key (default: a placeholder, for environments whose egress
// proxy injects the key) and the base URL (default https://api.typesafe.ai/v1).
func TestSystemOneLive(t *testing.T) {
	if os.Getenv("EZYAPPER_LIVE_JEV") != "1" {
		t.Skip("set EZYAPPER_LIVE_JEV=1 to call the live System One API")
	}
	baseURL := os.Getenv("EZYAPPER_JEV_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.typesafe.ai/v1"
	}
	apiKey := os.Getenv("EZYAPPER_JEV_API_KEY")
	if apiKey == "" {
		apiKey = "placeholder"
	}

	t0 := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

	scenarios := []struct {
		name    string
		recent  []ContextMessage
		current MessageInfo
		images  int
	}{
		{
			name: "addresses the bot directly",
			recent: []ContextMessage{
				{AuthorName: "bob", Content: "anyone up for ranked later?", Timestamp: at(0)},
				{AuthorName: "alice", Content: "maybe after dinner", Timestamp: at(1)},
			},
			current: MessageInfo{AuthorName: "alice", Content: "Ezy, can you explain what a goroutine is?", Timestamp: at(2)},
		},
		{
			name: "two users talking to each other",
			recent: []ContextMessage{
				{AuthorName: "alice", Content: "bob did you finish the report?", Timestamp: at(0)},
				{AuthorName: "bob", Content: "almost, I'll send it tonight", Timestamp: at(1), ReplyTo: "alice"},
			},
			current: MessageInfo{AuthorName: "alice", Content: "cool, thanks bob", Timestamp: at(2), ReplyTo: "bob"},
		},
		{
			name: "continues a conversation with the bot",
			recent: []ContextMessage{
				{AuthorName: "alice", Content: "Ezy what's the difference between a slice and an array in Go?", Timestamp: at(0)},
				{AuthorName: "Ezy", IsBot: true, Content: "An array has a fixed length that is part of its type; a slice is a view over an array that can grow.", Timestamp: at(1), ReplyTo: "alice"},
			},
			current: MessageInfo{AuthorName: "alice", Content: "makes sense. and how do maps work then?", Timestamp: at(2)},
		},
		{
			name: "open question to the channel",
			recent: []ContextMessage{
				{AuthorName: "carol", Content: "lol that meme", Timestamp: at(0)},
			},
			current: MessageInfo{AuthorName: "dave", Content: "does anyone know why go build says 'undefined: slices.Contains'?", Timestamp: at(1)},
		},
		{
			name:    "image only between users",
			recent:  []ContextMessage{{AuthorName: "bob", Content: "alice send the pic of your cat", Timestamp: at(0)}},
			current: MessageInfo{AuthorName: "alice", Content: "", Timestamp: at(1), ReplyTo: "bob"},
			images:  1,
		},
	}

	variants := []struct {
		name string
		cfg  config.SystemOneConfig
	}{
		{"instructions only", config.SystemOneConfig{
			Instructions: "Should the Discord bot Ezy send a reply to current_message?",
		}},
		{"with criteria", config.SystemOneConfig{
			Instructions:  "Should the Discord bot Ezy send a reply to current_message?",
			CriteriaTrue:  "current_message talks to Ezy, asks Ezy or the channel something Ezy can answer, or continues a conversation with Ezy",
			CriteriaFalse: "current_message is aimed at other people, is a reaction or small talk between other users, or needs no reply",
		}},
	}

	for _, v := range variants {
		cfg := v.cfg
		cfg.APIBaseURL, cfg.APIKey, cfg.Model = baseURL, apiKey, "jev-latest"
		cfg.Timeout, cfg.RetryCount, cfg.Threshold, cfg.MaxStateBytes = 20, 2, 0.7, 48000
		svc, err := NewSystemOneService(&cfg)
		if err != nil {
			t.Fatal(err)
		}
		probs := map[string]float64{}
		for _, sc := range scenarios {
			start := time.Now()
			res, err := svc.ShouldRespondWithInfo(context.Background(), "", sc.current, sc.images, sc.recent)
			if err != nil {
				t.Fatalf("[%s] %s: %v", v.name, sc.name, err)
			}
			p := res.Confidence
			if !res.ShouldRespond {
				p = 1 - res.Confidence
			}
			probs[sc.name] = p
			t.Logf("[%s] %-40s p(yes)=%.2f respond=%v (%s)", v.name, sc.name, p, res.ShouldRespond, time.Since(start).Round(time.Millisecond))
		}
		if probs["addresses the bot directly"] <= probs["two users talking to each other"] {
			t.Errorf("[%s] expected a direct address to score above two users talking: %.2f <= %.2f",
				v.name, probs["addresses the bot directly"], probs["two users talking to each other"])
		}
	}
}
