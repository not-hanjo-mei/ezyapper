// Package configtest gives tests the suggested config values from
// examples/config.yaml.example. The bot has no built-in defaults, so a test
// that needs a complete modality block takes its values from the example
// (the single place suggested values live) instead of from code.
//
// Only tests import this package.
package configtest

import (
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"ezyapper/internal/config"
)

var (
	once    sync.Once
	example *config.Config
	loadErr error
)

// ExamplePath returns the absolute path of examples/config.yaml.example.
func ExamplePath() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "examples", "config.yaml.example")
}

// Example loads examples/config.yaml.example once and returns a copy.
func Example(t testing.TB) config.Config {
	t.Helper()
	once.Do(func() { example, loadErr = config.Load(ExamplePath()) })
	if loadErr != nil {
		t.Fatalf("load %s: %v", ExamplePath(), loadErr)
	}
	return *example
}

// FillSuggested sets every empty non-endpoint field of m (prompts, limits,
// transports, audio mode and part type, video max_height, sticker cache,
// ffmpeg_path) to the example's suggested value. Enabled switches and model
// endpoint fields are left alone.
func FillSuggested(t testing.TB, m *config.ModalitiesConfig) {
	t.Helper()
	ex := Example(t).AI.Modalities
	str := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	num := func(dst *int, v int) {
		if *dst == 0 {
			*dst = v
		}
	}
	img := &m.Image
	str(&img.Prompt, ex.Image.Prompt)
	num(&img.MaxItems, ex.Image.MaxItems)
	if img.Transport == "" {
		img.Transport = ex.Image.Transport
	}

	au := &m.Audio
	if au.Mode == "" {
		au.Mode = ex.Audio.Mode
	}
	num(&au.MaxItems, ex.Audio.MaxItems)
	num(&au.MaxBytes, ex.Audio.MaxBytes)
	num(&au.MaxDurationSec, ex.Audio.MaxDurationSec)
	if au.PartType == "" {
		au.PartType = config.AudioPartURL // the example's "## part_type" suggestion
	}
	if au.Mode == config.AudioModeDescribe {
		str(&au.Prompt, SuggestedAudioDescribePrompt)
	}

	v := &m.Video
	str(&v.Prompt, ex.Video.Prompt)
	num(&v.MaxItems, ex.Video.MaxItems)
	num(&v.MaxBytes, ex.Video.MaxBytes)
	num(&v.MaxDurationSec, ex.Video.MaxDurationSec)
	if v.Transport == "" {
		v.Transport = ex.Video.Transport
	}
	num(&v.MaxHeight, SuggestedVideoMaxHeight)

	st := &m.Sticker
	str(&st.Prompt, ex.Sticker.Prompt)
	num(&st.MaxItems, ex.Sticker.MaxItems)
	num(&st.CacheTTLMin, ex.Sticker.CacheTTLMin)
	num(&st.CacheMaxEntries, ex.Sticker.CacheMaxEntries)

	str(&m.FFmpegPath, "ffmpeg")
}

// Suggestions the example only shows commented out ("##"), because they
// apply to a mode the example does not enable.
const (
	SuggestedAudioDescribePrompt = "Transcribe the speech in this audio verbatim, in its original language. If there is no speech, describe the sound or music in one sentence. Reply with the transcript or description only."
	SuggestedVideoMaxHeight      = 480
)
