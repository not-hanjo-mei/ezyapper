package web

import (
	"net/http"
	"sync/atomic"
)

// renderStandardPage renders a page using the standard boilerplate:
// CSRFToken from context + activeNavItems + flashFromCookie + RenderPage.
// Use this in every GET handler that renders a standard page.
func renderStandardPage(w http.ResponseWriter, r *http.Request, ts *TemplateSet, pageName string, data interface{}) {
	csrfToken := CSRFTokenFromContext(r.Context())
	navItems := activeNavItems(pageName)
	flash := flashFromCookie(r, pageName)
	pageData := &PageData{
		CSRFToken: csrfToken,
		NavItems:  navItems,
		Flash:     flash,
		Data:      data,
	}
	RenderPage(w, ts, pageName, pageData)
}

// activeNavItems returns the standard navigation bar items, with the item
// matching currentPage marked as active by setting its Active field to true.
func activeNavItems(currentPage string) []NavItem {
	return []NavItem{
		{Label: "Dashboard", Href: "/", Icon: "dashboard", Active: currentPage == "dashboard"},
		{Label: "Configuration", Href: "/config", Icon: "settings", Active: currentPage == "config"},
		{Label: "Memories", Href: "/memories", Icon: "memory", Active: currentPage == "memories"},
		{Label: "Profiles", Href: "/profiles", Icon: "person", Active: currentPage == "profiles"},
		{Label: "Channels", Href: "/channels", Icon: "forum", Active: currentPage == "channels"},
		{Label: "Plugins", Href: "/plugins", Icon: "extension", Active: currentPage == "plugins"},
		{Label: "Logs", Href: "/logs", Icon: "description", Active: currentPage == "logs"},
	}
}
