package httpcapture

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecorderCapturesRequestAndRedactsKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"m"`) {
			t.Errorf("server did not receive the original body: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	client := NewRecorder(&buf).WrapClient(&http.Client{}, "llm")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != `{"ok":true}` {
		t.Fatalf("caller must still read the response body, got %q", got)
	}

	var e Entry
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &e); err != nil {
		t.Fatalf("invalid capture line %q: %v", buf.String(), err)
	}
	if e.Label != "llm" || e.Status != 200 || string(e.ReqBody) != `{"model":"m"}` || string(e.RespBody) != `{"ok":true}` {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if e.ReqHeaders["Authorization"] != "<redacted>" || strings.Contains(buf.String(), "secret") {
		t.Fatal("authorization header must be redacted")
	}
}

func TestWrapSetsUserAgent(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("User-Agent"))
	}))
	defer srv.Close()
	c := Wrap(&http.Client{}, "t")
	if _, err := c.Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("User-Agent", "custom/1")
	if _, err := c.Do(req); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != UserAgent || got[1] != "custom/1" {
		t.Fatalf("user agents = %v", got)
	}
}
