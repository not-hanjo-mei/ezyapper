package web

import (
	"encoding/base64"
	"net/http"
)

// cookiePrefix builds the flash cookie name prefix from the given page prefix.
// An empty prefix yields "flash" (for the config/plugins pages).
// A non-empty prefix yields "<prefix>_flash" (for channel/memory/profile pages).
func cookiePrefix(prefix string) string {
	if prefix == "" {
		return "flash"
	}
	return prefix + "_flash"
}

// setFlashCookie sets a flash message cookie pair for the given prefix.
// Two cookies are set: <base>_type and <base>_msg (base64-encoded).
// Cookies are HttpOnly, Secure, SameSiteStrict, and expire after 60 seconds.
func setFlashCookie(w http.ResponseWriter, prefix, flashType, message string) {
	base := cookiePrefix(prefix)

	http.SetCookie(w, &http.Cookie{
		Name:     base + "_type",
		Value:    flashType,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   60,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     base + "_msg",
		Value:    base64.URLEncoding.EncodeToString([]byte(message)),
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   60,
	})
}

// flashFromCookie reads flash message data from cookies for the given prefix.
// Returns nil if either cookie is missing or the message cannot be decoded.
// Cookies are NOT cleared — they self-expire after 60 seconds.
func flashFromCookie(r *http.Request, prefix string) *FlashMessage {
	base := cookiePrefix(prefix)

	typeCookie, err := r.Cookie(base + "_type")
	if err != nil {
		return nil
	}
	msgCookie, err := r.Cookie(base + "_msg")
	if err != nil {
		return nil
	}
	msgBytes, err := base64.URLEncoding.DecodeString(msgCookie.Value)
	if err != nil {
		return nil
	}
	return &FlashMessage{
		Type:    typeCookie.Value,
		Message: string(msgBytes),
	}
}
