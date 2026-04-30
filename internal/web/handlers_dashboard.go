package web

import (
	"net/http"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
)

// dashboardData wraps GlobalStats with computed Uptime for the template.
type dashboardData struct {
	memory.GlobalStats
	Uptime int64
}

// DashboardHandler returns an http.HandlerFunc for GET / that renders the
// dashboard page with live statistics from Qdrant. Uptime is computed from
// the provided startTime. On stats failure, the page renders with zero values.
func DashboardHandler(stats *StatsProvider, startTime time.Time, ts *TemplateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx := r.Context()
		gs, err := stats.GetDashboardStats(ctx)
		if err != nil {
			logger.Errorf("[Web] failed to fetch dashboard stats: %v", err)
		}
		csrfToken := CSRFTokenFromContext(ctx)

		data := dashboardData{
			GlobalStats: gs,
			Uptime:      int64(time.Since(startTime).Seconds()),
		}

		navItems := []NavItem{
			{Label: "Dashboard", Href: "/", Icon: "dashboard", Active: true},
			{Label: "Configuration", Href: "/config", Icon: "settings", Active: false},
			{Label: "Memories", Href: "/memories", Icon: "memory", Active: false},
			{Label: "Profiles", Href: "/profiles", Icon: "person", Active: false},
			{Label: "Channels", Href: "/channels", Icon: "forum", Active: false},
			{Label: "Plugins", Href: "/plugins", Icon: "extension", Active: false},
			{Label: "Logs", Href: "/logs", Icon: "description", Active: false},
		}

		pageData := &PageData{
			Title:     "Dashboard",
			ActiveNav: "dashboard",
			CSRFToken: csrfToken,
			Data:      data,
			NavItems:  navItems,
		}

		RenderPage(w, ts, "dashboard", pageData)
	}
}
