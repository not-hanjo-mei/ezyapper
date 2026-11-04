package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// go-openai encodes ChatCompletionRequest.Temperature with omitempty, so a
// temperature of 0 is left out of the request and the provider applies its
// own default instead. A temperature key that is set to 0 in the config
// (config.LLMConfig.ExplicitZeroTemperature) must reach the API as 0, so the
// client's HTTP transport adds "temperature": 0 to chat completion requests
// that do not carry one.

// withZeroTemperature returns a copy of c whose transport sends
// "temperature": 0 with every chat completion request that has no
// temperature.
func withZeroTemperature(c *http.Client) *http.Client {
	out := *c
	inner := c.Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	out.Transport = zeroTemperatureTransport{inner: inner}
	return &out
}

type zeroTemperatureTransport struct{ inner http.RoundTripper }

func (t zeroTemperatureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.Body == nil || !strings.HasSuffix(req.URL.Path, "/chat/completions") {
		return t.inner.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return nil, err
	}
	if patched, ok := addZeroTemperature(body); ok {
		body = patched
	}
	r := req.Clone(req.Context())
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return t.inner.RoundTrip(r)
}

// addZeroTemperature adds "temperature": 0 to a JSON object body that has no
// temperature key. Other bodies are left alone (ok false).
func addZeroTemperature(body []byte) ([]byte, bool) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, false
	}
	if _, has := doc["temperature"]; has {
		return nil, false
	}
	doc["temperature"] = json.RawMessage("0")
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false
	}
	return out, true
}
