// Package web provides an HTTP API server and WebUI for managing the bot.
package web

// PageData holds common data passed to every page template.
type PageData struct {
	Title     string
	ActiveNav string
	CSRFToken string
	Flash     *FlashMessage
	Data      any
	NavItems  []NavItem
	BuildTag  string
}

// FlashMessage represents a one-time notification displayed on a page.
type FlashMessage struct {
	Type    string // "success", "error", "info", "warning"
	Message string
}

// NavItem represents a single entry in the navigation bar.
type NavItem struct {
	Label  string
	Href   string
	Icon   string
	Active bool
}
