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
			logger.Errorf("[web] failed to fetch dashboard stats: %v", err)
		}
		data := dashboardData{
			GlobalStats: gs,
			Uptime:      int64(time.Since(startTime).Seconds()),
		}

		renderStandardPage(w, r, ts, "dashboard", data)
	}
}
