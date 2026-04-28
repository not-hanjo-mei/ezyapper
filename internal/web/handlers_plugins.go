package web

import (
	"net/http"
	"strings"

	"ezyapper/internal/plugin"
)

type pluginManager interface {
	ListPluginsExt() []plugin.InfoExt
	EnablePlugin(name string) error
	DisablePlugin(name string) error
}

type pluginsPageData struct {
	Plugins []plugin.InfoExt
}

// PluginsHandler returns an http.HandlerFunc for the plugins management page.
// GET  /plugins         — list all plugins with enabled/disabled status
// POST /plugins/toggle  — enable or disable a plugin by name
func PluginsHandler(mgr pluginManager, refresher PluginToolRefresher, ts *TemplateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case r.Method == http.MethodGet && path == "/plugins":
			handlePluginsGET(w, r, mgr, ts)
		case r.Method == http.MethodPost && path == "/plugins/toggle":
			handlePluginsToggle(w, r, mgr, refresher, ts)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func handlePluginsGET(w http.ResponseWriter, r *http.Request, mgr pluginManager, ts *TemplateSet) {
	ctx := r.Context()
	csrfToken := CSRFTokenFromContext(ctx)
	flash := flashFromCookiePlugins(r)

	plugins := mgr.ListPluginsExt()

	pd := &pluginsPageData{
		Plugins: plugins,
	}

	navItems := []NavItem{
		{Label: "Dashboard", Href: "/", Icon: "dashboard"},
		{Label: "Configuration", Href: "/config", Icon: "settings"},
		{Label: "Memories", Href: "/memories", Icon: "memory"},
		{Label: "Profiles", Href: "/profiles", Icon: "person"},
		{Label: "Channels", Href: "/channels", Icon: "forum"},
		{Label: "Plugins", Href: "/plugins", Icon: "extension", Active: true},
		{Label: "Logs", Href: "/logs", Icon: "description"},
	}

	RenderPage(w, ts, "plugins", &PageData{
		Title:        "Plugins",
		ActiveNav:    "plugins",
		CSRFToken:    csrfToken,
		Flash:        flash,
		Data:         pd,
		NavItems:  navItems,
	})
}

func handlePluginsToggle(w http.ResponseWriter, r *http.Request, mgr pluginManager, refresher PluginToolRefresher, ts *TemplateSet) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form data", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	action := strings.TrimSpace(r.FormValue("action"))

	if name == "" {
		http.Error(w, "Plugin name is required", http.StatusBadRequest)
		return
	}

	var err error
	switch action {
	case "enable":
		err = mgr.EnablePlugin(name)
	case "disable":
		err = mgr.DisablePlugin(name)
	default:
		http.Error(w, "Invalid action: must be 'enable' or 'disable'", http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, "Plugin not found: "+name, http.StatusNotFound)
		return
	}

	refresher.RefreshPluginTools()

	setFlashCookie(w, "success", "Plugin "+action+"d: "+name)
	http.Redirect(w, r, "/plugins", http.StatusSeeOther)
}

func setFlashCookiePlugins(w http.ResponseWriter, flashType, message string) {
	setFlashCookie(w, flashType, message)
}

func flashFromCookiePlugins(r *http.Request) *FlashMessage {
	return flashFromCookie(r)
}
