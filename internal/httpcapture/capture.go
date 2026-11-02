// Package httpcapture records outgoing HTTP exchanges (model, embedding and
// vision API calls) to a JSONL file so the exact assembled requests can be
// inspected. It is off unless EZYAPPER_CAPTURE_DIR is set.
package httpcapture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EnvDir is the environment variable that enables capture. Its value is the
// directory the capture file is written to.
const EnvDir = "EZYAPPER_CAPTURE_DIR"

// maxBodyBytes caps each recorded body; base64 images can be huge.
const maxBodyBytes = 1 << 20

var sensitiveHeaders = map[string]bool{
	"authorization": true, "api-key": true, "x-api-key": true, "cookie": true, "set-cookie": true,
}

// Entry is one recorded request/response pair.
type Entry struct {
	Time       time.Time         `json:"time"`
	Label      string            `json:"label"`
	Method     string            `json:"method"`
	URL        string            `json:"url"`
	ReqHeaders map[string]string `json:"request_headers"`
	ReqBody    json.RawMessage   `json:"request_body,omitempty"`
	ReqText    string            `json:"request_text,omitempty"`
	Status     int               `json:"status,omitempty"`
	RespBody   json.RawMessage   `json:"response_body,omitempty"`
	RespText   string            `json:"response_text,omitempty"`
	Error      string            `json:"error,omitempty"`
	DurationMs int64             `json:"duration_ms"`
}

// Recorder writes entries to a single JSONL file.
type Recorder struct {
	mu sync.Mutex
	w  io.Writer
}

var (
	defaultOnce sync.Once
	defaultRec  *Recorder
)

// Default returns the process-wide recorder, or nil when capture is disabled.
func Default() *Recorder {
	defaultOnce.Do(func() {
		dir := strings.TrimSpace(os.Getenv(EnvDir))
		if dir == "" {
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "[httpcapture] disabled: %v\n", err)
			return
		}
		name := filepath.Join(dir, "capture-"+time.Now().UTC().Format("20060102T150405Z")+".jsonl")
		f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[httpcapture] disabled: %v\n", err)
			return
		}
		fmt.Fprintf(os.Stderr, "[httpcapture] recording HTTP exchanges to %s\n", name)
		defaultRec = &Recorder{w: f}
	})
	return defaultRec
}

// NewRecorder records to w (used by tests).
func NewRecorder(w io.Writer) *Recorder { return &Recorder{w: w} }

func (r *Recorder) write(e Entry) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.w.Write(append(b, '\n'))
}

// Wrap returns an http.Client whose transport records through the default
// recorder. When capture is disabled the client is returned unchanged.
//
// Wrap also gives every request an explicit User-Agent (UserAgent) unless
// the caller set one. Go's default "Go-http-client/x" is blocked outright by
// Cloudflare-fronted OpenAI-compatible gateways (error 1010), which made
// model and embedding calls fail with 403 on such endpoints.
func Wrap(c *http.Client, label string) *http.Client {
	if c == nil {
		return c
	}
	c = withUserAgent(c)
	rec := Default()
	if rec == nil {
		return c
	}
	return rec.WrapClient(c, label)
}

// UserAgent is sent on outgoing model/API requests that do not set their own.
const UserAgent = "EZyapper/1.0 (+https://github.com/not-hanjo-mei/ezyapper)"

func withUserAgent(c *http.Client) *http.Client {
	cp := *c
	base := cp.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	cp.Transport = uaTransport{base: base}
	return &cp
}

type uaTransport struct{ base http.RoundTripper }

func (t uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if ua := req.Header.Get("User-Agent"); ua == "" || strings.HasPrefix(ua, "Go-http-client") {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", UserAgent)
	}
	return t.base.RoundTrip(req)
}

// WrapClient returns a copy of c that records through r.
func (r *Recorder) WrapClient(c *http.Client, label string) *http.Client {
	cp := *c
	base := cp.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	cp.Transport = &transport{base: base, rec: r, label: label}
	return &cp
}

type transport struct {
	base  http.RoundTripper
	rec   *Recorder
	label string
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	e := Entry{Time: start.UTC(), Label: t.label, Method: req.Method, URL: req.URL.String(), ReqHeaders: redact(req.Header)}

	if req.Body != nil && req.Body != http.NoBody {
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		e.ReqBody, e.ReqText = encodeBody(body)
	}

	resp, err := t.base.RoundTrip(req)
	e.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		e.Error = err.Error()
		t.rec.write(e)
		return nil, err
	}

	e.Status = resp.StatusCode
	if resp.Body != nil && !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if readErr != nil {
			e.Error = readErr.Error()
		}
		if isText(resp.Header.Get("Content-Type")) {
			e.RespBody, e.RespText = encodeBody(body)
		} else {
			e.RespText = fmt.Sprintf("<%d bytes of %s>", len(body), resp.Header.Get("Content-Type"))
		}
	}
	t.rec.write(e)
	return resp, nil
}

func isText(ct string) bool {
	ct = strings.ToLower(ct)
	return ct == "" || strings.Contains(ct, "json") || strings.HasPrefix(ct, "text/")
}

func encodeBody(body []byte) (json.RawMessage, string) {
	if len(body) > maxBodyBytes {
		return nil, string(body[:maxBodyBytes]) + fmt.Sprintf("... (%d bytes total)", len(body))
	}
	if json.Valid(body) {
		return json.RawMessage(body), ""
	}
	return nil, string(body)
}

func redact(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if sensitiveHeaders[strings.ToLower(k)] {
			out[k] = "<redacted>"
			continue
		}
		out[k] = strings.Join(v, ", ")
	}
	return out
}
