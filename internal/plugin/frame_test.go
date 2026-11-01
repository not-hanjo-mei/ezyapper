package plugin

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFrameDecoderRecoversAfterBadFrame(t *testing.T) {
	dec := NewFrameDecoder(strings.NewReader("garbage line\n\n{\"a\":1}\n{\"a\":2}"))
	var v struct{ A int }

	err := dec.Decode(&v)
	if !IsMalformedFrame(err) {
		t.Fatalf("expected malformed frame error, got %v", err)
	}
	if err := dec.Decode(&v); err != nil || v.A != 1 {
		t.Fatalf("expected a=1 after recovery, got a=%d err=%v", v.A, err)
	}
	if err := dec.Decode(&v); err != nil || v.A != 2 {
		t.Fatalf("expected a=2 from unterminated last line, got a=%d err=%v", v.A, err)
	}
	if err := dec.Decode(&v); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestFrameDecoderLongLine(t *testing.T) {
	long := `{"s":"` + strings.Repeat("x", 200000) + `"}` + "\n"
	dec := NewFrameDecoder(strings.NewReader(long))
	var v struct{ S string }
	if err := dec.Decode(&v); err != nil || len(v.S) != 200000 {
		t.Fatalf("expected long frame to decode, len=%d err=%v", len(v.S), err)
	}
}
