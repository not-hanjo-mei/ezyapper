package media

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// permanentError marks a failure that will not go away by trying again
// (size or duration over the limit, a transcode error).
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// permanent marks err as permanent unless ctx was cancelled or timed out
// (a killed ffmpeg reports an exec error, not the context's).
func permanent(ctx context.Context, err error) error {
	if err == nil || (ctx != nil && ctx.Err() != nil) {
		return err
	}
	return &permanentError{err: err}
}

var (
	// downloadStatusRe matches FetchMedia's "download media: status 404" and
	// discordgo's "HTTP 404 Not Found".
	downloadStatusRe = regexp.MustCompile(`(?:download media: status|HTTP) (\d{3})\b`)
	// modelStatusRe matches the model and ASR clients' "status code 400".
	modelStatusRe = regexp.MustCompile(`status code (\d{3})\b`)
)

// permanentModelStatus are the model / ASR answers that mean the request
// itself is wrong for this media: retrying sends the same request again.
var permanentModelStatus = map[int]bool{400: true, 404: true, 413: true, 415: true, 422: true}

// IsPermanent reports whether a media processing error is permanent and may
// be cached for FailureTTL: size or duration over the limit, a wrong content
// type, a download answered 4xx (except 408 and 429), a model answer of
// 400/404/413/415/422, or a transcode error. Timeouts, rate limits, server
// errors, cancellation and empty model output are not.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var cf *cachedFailure
	if errors.As(err, &cf) {
		return true
	}
	var pe *permanentError
	if errors.As(err, &pe) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, transient := range []string{"timeout", "deadline exceeded", "context canceled", "too many requests"} {
		if strings.Contains(msg, transient) {
			return false
		}
	}
	if strings.Contains(msg, "unexpected content type") || strings.Contains(msg, "media too large") {
		return true
	}
	if m := downloadStatusRe.FindStringSubmatch(err.Error()); m != nil {
		code, _ := strconv.Atoi(m[1])
		return code >= 400 && code < 500 && code != 408 && code != 429
	}
	if m := modelStatusRe.FindStringSubmatch(err.Error()); m != nil {
		code, _ := strconv.Atoi(m[1])
		return permanentModelStatus[code]
	}
	return false
}
