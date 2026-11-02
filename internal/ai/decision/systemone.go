package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
	"ezyapper/internal/httpcapture"
	"ezyapper/internal/logger"
	"ezyapper/internal/retry"
)

// systemOneQuestionID is the id of the single noul question we ask. Ids are
// not shown to the model; the answer comes back under the same id.
const systemOneQuestionID = "should_respond"

// maxSystemOneResponseBytes bounds how much of a response body is read.
const maxSystemOneResponseBytes = 1 << 20

// truncationMarker is appended to a current message cut to fit the budget.
const truncationMarker = " …[truncated]"

// SystemOneService is the "system_one" provider. It asks a System One
// decision model (TypeSafe Jev) one yes/no ("noul") question about a JSON
// state built from the conversation and replies when P(yes) reaches the
// configured threshold. Unlike the llm provider there is no generated text
// to parse: the answer is a calibrated probability.
type SystemOneService struct {
	cfg        *config.SystemOneConfig
	endpoint   string
	httpClient *http.Client
}

var _ Decider = (*SystemOneService)(nil)

// NewSystemOneService builds the provider. The HTTP client is wrapped with
// httpcapture under "decision:<model>", like the llm provider.
func NewSystemOneService(cfg *config.SystemOneConfig) (*SystemOneService, error) {
	if cfg.APIBaseURL == "" {
		return nil, fmt.Errorf("decision.system_one.api_base_url is required when decision is enabled")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("decision.system_one.api_key is required when decision is enabled")
	}
	httpTimeout := time.Duration(cfg.Timeout) * time.Second
	return &SystemOneService{
		cfg:        cfg,
		endpoint:   strings.TrimRight(cfg.APIBaseURL, "/") + "/systemone",
		httpClient: httpcapture.Wrap(&http.Client{Timeout: httpTimeout}, "decision:"+cfg.Model),
	}, nil
}

// ── Wire types ────────────────────────────────────────────────────────────

type systemOneCriteria struct {
	True  string `json:"true"`
	False string `json:"false"`
}

type systemOneQuestion struct {
	Type         string             `json:"type"`
	Instructions string             `json:"instructions"`
	Criteria     *systemOneCriteria `json:"criteria,omitempty"`
}

type systemOneRequest struct {
	Model     string                       `json:"model"`
	State     json.RawMessage              `json:"state"`
	Questions map[string]systemOneQuestion `json:"questions"`
}

type systemOneAnswer struct {
	Type string   `json:"type"`
	Noul *float64 `json:"noul"`
}

type systemOneResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]systemOneAnswer `json:"answers"`
}

// stateMessage is one chat message in the decision state.
type stateMessage struct {
	Author    string `json:"author"`
	IsBot     bool   `json:"is_bot"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
	ReplyTo   string `json:"reply_to,omitempty"`
}

type stateCurrentMessage struct {
	stateMessage
	ImageCount int `json:"image_count"`
}

// decisionState is the JSON object sent as the System One "state".
// Field order is the encoding order: history first, then the message the
// question is about.
type decisionState struct {
	RecentMessages       []stateMessage      `json:"recent_messages"`
	OlderMessagesOmitted int                 `json:"older_messages_omitted,omitempty"`
	CurrentMessage       stateCurrentMessage `json:"current_message"`
}

// ── Errors ────────────────────────────────────────────────────────────────

// SystemOneStatusError is a non-200 HTTP response.
type SystemOneStatusError struct {
	StatusCode int
	Body       string
}

func (e *SystemOneStatusError) Error() string {
	return fmt.Sprintf("system one API returned HTTP %d: %s", e.StatusCode, e.Body)
}

// Retryable reports whether the status is transient: rate limited (429),
// overloaded (529) or another server error. 4xx request errors (bad state,
// context too long, bad key, validation) fail identically on retry.
func (e *SystemOneStatusError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// errSystemOneProtocol marks a 200 response we could not use (bad JSON,
// missing answer, probability out of range).
var errSystemOneProtocol = errors.New("invalid system one response")

func isRetryableSystemOneError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var se *SystemOneStatusError
	if errors.As(err, &se) {
		return se.Retryable()
	}
	var serr *stateBudgetError
	if errors.As(err, &serr) {
		return false
	}
	// Protocol errors, per-attempt timeouts and transport errors are
	// worth another attempt; the parent deadline still bounds the total.
	return true
}

// stateBudgetError means the state cannot fit max_state_bytes at all.
type stateBudgetError struct{ size, budget int }

func (e *stateBudgetError) Error() string {
	return fmt.Sprintf("decision state needs %d bytes even with no history and empty content, max_state_bytes is %d", e.size, e.budget)
}

// ── Decision ──────────────────────────────────────────────────────────────

// ShouldRespondWithInfo implements Decider. botName is ignored: the
// instructions and criteria are literal config text.
func (s *SystemOneService) ShouldRespondWithInfo(ctx context.Context, _ string, msgInfo MessageInfo, imageCount int, recentMessages []ContextMessage) (*DecisionResult, error) {
	state, omitted, err := buildDecisionState(msgInfo, imageCount, recentMessages, s.cfg.MaxStateBytes)
	if err != nil {
		return nil, fmt.Errorf("system one decision: %w", err)
	}
	logger.Debugf("[decision] system_one state: %d bytes, %d context messages (%d older omitted), %d image(s)",
		len(state), len(recentMessages)-omitted, omitted, imageCount)

	question := systemOneQuestion{Type: "noul", Instructions: s.cfg.Instructions}
	if s.cfg.CriteriaTrue != "" || s.cfg.CriteriaFalse != "" {
		question.Criteria = &systemOneCriteria{True: s.cfg.CriteriaTrue, False: s.cfg.CriteriaFalse}
	}
	body, err := json.Marshal(systemOneRequest{
		Model:     s.cfg.Model,
		State:     state,
		Questions: map[string]systemOneQuestion{systemOneQuestionID: question},
	})
	if err != nil {
		return nil, fmt.Errorf("system one decision: encode request: %w", err)
	}

	p, err := retry.Retry(ctx, s.cfg.RetryCount, func(ctx context.Context) (float64, error) {
		return s.ask(ctx, body)
	},
		retry.WithBaseDelay(250*time.Millisecond),
		retry.WithMaxDelay(2*time.Second),
		retry.WithErrorClassifier(isRetryableSystemOneError),
	)
	if err != nil {
		return nil, fmt.Errorf("system one decision call failed: %w", err)
	}

	result := systemOneResult(p, s.cfg.Threshold)
	logger.Debugf("[decision] system_one result: should_respond=%v, %s", result.ShouldRespond, result.Reason)
	return result, nil
}

// systemOneResult turns P(yes) into a verdict. Confidence is the
// probability of the verdict taken (p when replying, 1-p when not), which
// keeps the llm provider's meaning of "how sure is this verdict" on a 0..1
// scale. A "no" just under a high threshold therefore reports a confidence
// below 0.5: the model leaned yes, the threshold said no.
func systemOneResult(p, threshold float64) *DecisionResult {
	respond := p >= threshold
	confidence := p
	if !respond {
		confidence = 1 - p
	}
	return &DecisionResult{
		ShouldRespond: respond,
		Reason:        fmt.Sprintf("p(yes)=%.2f threshold=%.2f", p, threshold),
		Confidence:    confidence,
	}
}

// ask performs one HTTP attempt and returns P(yes).
func (s *SystemOneService) ask(ctx context.Context, body []byte) (float64, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.Timeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		if ai.IsTimeoutLikeError(err) {
			s.httpClient.CloseIdleConnections()
		}
		return 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSystemOneResponseBytes))
	if err != nil {
		return 0, fmt.Errorf("read system one response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, &SystemOneStatusError{StatusCode: resp.StatusCode, Body: truncateForLog(string(raw), 500)}
	}

	var parsed systemOneResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return 0, fmt.Errorf("%w: %v", errSystemOneProtocol, err)
	}
	ans, ok := parsed.Answers[systemOneQuestionID]
	if !ok || ans.Noul == nil {
		return 0, fmt.Errorf("%w: no noul answer for %q", errSystemOneProtocol, systemOneQuestionID)
	}
	p := *ans.Noul
	if math.IsNaN(p) || p < 0 || p > 1 {
		return 0, fmt.Errorf("%w: noul %v out of range [0,1]", errSystemOneProtocol, p)
	}
	logger.Debugf("[decision] system_one model=%s p(yes)=%.4f", parsed.Model, p)
	return p, nil
}

// ── State ─────────────────────────────────────────────────────────────────

// buildDecisionState encodes the conversation as JSON within maxBytes.
// Context messages are sorted oldest first (Discord pages arrive newest
// first) and the oldest are dropped until the state fits. If the current
// message alone is still too large, its content is cut. It returns the
// encoded state and how many context messages were dropped.
func buildDecisionState(msgInfo MessageInfo, imageCount int, recent []ContextMessage, maxBytes int) (json.RawMessage, int, error) {
	history := make([]ContextMessage, len(recent))
	copy(history, recent)
	sort.SliceStable(history, func(i, j int) bool { return history[i].Timestamp.Before(history[j].Timestamp) })

	entries := make([]stateMessage, len(history))
	sizes := make([]int, len(history))
	for i, m := range history {
		entries[i] = stateMessage{
			Author:    m.AuthorName,
			IsBot:     m.IsBot,
			Content:   m.Content,
			Timestamp: formatStateTime(m.Timestamp),
			ReplyTo:   m.ReplyTo,
		}
		b, err := encodeJSON(entries[i])
		if err != nil {
			return nil, 0, err
		}
		sizes[i] = len(b) + 1 // trailing comma
	}

	state := decisionState{
		RecentMessages: []stateMessage{},
		CurrentMessage: stateCurrentMessage{
			stateMessage: stateMessage{
				Author:    msgInfo.AuthorName,
				IsBot:     msgInfo.IsBot,
				Content:   msgInfo.Content,
				Timestamp: formatStateTime(msgInfo.Timestamp),
				ReplyTo:   msgInfo.ReplyTo,
			},
			ImageCount: imageCount,
		},
	}

	// Estimate the first kept message from per-entry sizes, then confirm
	// with exact encodings (the omitted counter adds a few bytes the
	// estimate does not include, so the estimate never drops too many).
	base, err := encodeJSON(state)
	if err != nil {
		return nil, 0, err
	}
	// sizes include a separating comma per entry; the first kept entry
	// needs none, hence the -1.
	start, total := len(entries), len(base)-1
	for start > 0 && total+sizes[start-1] <= maxBytes {
		start--
		total += sizes[start]
	}
	for {
		state.RecentMessages = entries[start:]
		state.OlderMessagesOmitted = start
		out, err := encodeJSON(state)
		if err != nil {
			return nil, 0, err
		}
		if len(out) <= maxBytes {
			return out, start, nil
		}
		if start == len(entries) {
			break
		}
		start++
	}

	// No history left and still over budget: cut the current message.
	content := state.CurrentMessage.Content
	for content != "" {
		out, err := encodeJSON(state)
		if err != nil {
			return nil, 0, err
		}
		over := len(out) - maxBytes
		if over <= 0 {
			return out, start, nil
		}
		keep := len(strings.TrimSuffix(content, truncationMarker)) - over - len(truncationMarker)
		if keep <= 0 {
			content = ""
		} else {
			content = cutUTF8(strings.TrimSuffix(content, truncationMarker), keep) + truncationMarker
		}
		state.CurrentMessage.Content = content
	}
	out, err := encodeJSON(state)
	if err != nil {
		return nil, 0, err
	}
	if len(out) > maxBytes {
		return nil, 0, &stateBudgetError{size: len(out), budget: maxBytes}
	}
	return out, start, nil
}

// encodeJSON marshals without HTML escaping, so <, > and & in chat text
// count as one byte each instead of six.
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func formatStateTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// cutUTF8 returns at most n bytes of s without splitting a rune.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func truncateForLog(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return cutUTF8(s, n) + "…"
}
