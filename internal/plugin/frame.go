package plugin

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxFrameBytes is the largest single JSON-RPC frame accepted on stdio.
// Larger frames are discarded and reported as malformed.
const MaxFrameBytes = 64 << 20

// MalformedFrameError reports a frame that could not be decoded as JSON.
// It is recoverable: the decoder has already advanced past the bad frame,
// so the caller can log it and keep reading.
type MalformedFrameError struct {
	// Excerpt is the beginning of the offending frame, truncated for logging.
	Excerpt string
	// Size is the full length of the offending frame in bytes.
	Size int
	Err  error
}

func (e *MalformedFrameError) Error() string {
	return fmt.Sprintf("malformed jsonrpc frame (%d bytes): %v: %q", e.Size, e.Err, e.Excerpt)
}

func (e *MalformedFrameError) Unwrap() error { return e.Err }

// IsMalformedFrame reports whether err is a recoverable malformed-frame error.
func IsMalformedFrame(err error) bool {
	var mf *MalformedFrameError
	return errors.As(err, &mf)
}

// FrameDecoder reads newline-delimited JSON frames. Unlike json.Decoder, a
// syntax error in one frame does not poison the stream: the bad line is
// skipped and the next Decode call starts at the following line.
type FrameDecoder struct {
	br *bufio.Reader
}

// NewFrameDecoder wraps r in a resilient newline-delimited JSON decoder.
func NewFrameDecoder(r io.Reader) *FrameDecoder {
	return &FrameDecoder{br: bufio.NewReaderSize(r, 64*1024)}
}

// readLine returns the next line without its trailing newline. Lines longer
// than MaxFrameBytes are drained and reported via the tooLarge flag.
func (d *FrameDecoder) readLine() (line []byte, size int, tooLarge bool, err error) {
	var buf bytes.Buffer
	for {
		chunk, readErr := d.br.ReadSlice('\n')
		size += len(chunk)
		if !tooLarge {
			if buf.Len()+len(chunk) > MaxFrameBytes {
				tooLarge = true
				// Keep the head for the log excerpt only.
				if buf.Len() < 256 {
					buf.Write(chunk[:min(len(chunk), 256-buf.Len())])
				}
			} else {
				buf.Write(chunk)
			}
		}
		if readErr == nil {
			return bytes.TrimRight(buf.Bytes(), "\r\n"), size, tooLarge, nil
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		// EOF or a real read error. Return whatever was collected first.
		if buf.Len() > 0 && errors.Is(readErr, io.EOF) {
			return bytes.TrimRight(buf.Bytes(), "\r\n"), size, tooLarge, nil
		}
		return nil, size, tooLarge, readErr
	}
}

// Decode reads the next non-empty frame into v. It returns io.EOF when the
// stream ends cleanly, a *MalformedFrameError for a bad frame (recoverable),
// or another error when the underlying reader fails.
func (d *FrameDecoder) Decode(v any) error {
	for {
		line, size, tooLarge, err := d.readLine()
		if err != nil {
			return err
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		if tooLarge {
			return &MalformedFrameError{Excerpt: excerpt(trimmed), Size: size, Err: fmt.Errorf("frame exceeds %d bytes", MaxFrameBytes)}
		}
		if err := json.Unmarshal(trimmed, v); err != nil {
			return &MalformedFrameError{Excerpt: excerpt(trimmed), Size: size, Err: err}
		}
		return nil
	}
}

func excerpt(b []byte) string {
	const limit = 200
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "..."
}
