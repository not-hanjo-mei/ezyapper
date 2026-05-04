package web

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
)

type memoryDisplayEntry struct {
	ID         string
	UserID     string
	MemoryType string
	Content    string
	Summary    string
	Keywords   []string
	Confidence float64
	CreatedAt  string
}

type memoriesPageData struct {
	UserID   string
	Memories []memoryDisplayEntry
	Count    int
	Searched bool
	Error    string
}

// MemoriesHandler returns an http.HandlerFunc for the memories browser page.
// GET  /memories          — search form with no results
// GET  /memories?userID=X — list memories for user X
// POST /memories/delete   — delete a memory (ownership-verified)
func MemoriesHandler(cfgStore *atomic.Value, memStore memory.MemoryStore, ts *TemplateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case r.Method == http.MethodGet && path == "/memories":
			handleMemoriesGET(w, r, cfgStore, memStore, ts)
		case r.Method == http.MethodPost && path == "/memories/delete":
			handleMemoriesDelete(w, r, memStore)
		default:
			http.Error(w, "Not found", http.StatusNotFound)
		}
	}
}

func handleMemoriesGET(w http.ResponseWriter, r *http.Request, cfgStore *atomic.Value, memStore memory.MemoryStore, ts *TemplateSet) {
	ctx := r.Context()
	cfg, ok := cfgStore.Load().(*config.Config)
	if !ok {
		http.Error(w, "Internal configuration error", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("userID"))
	entries := []memoryDisplayEntry{}
	var searched bool
	var errorMsg string

	if userID != "" {
		searched = true
		memories, err := memStore.GetMemories(ctx, userID, cfg.Web.MemoriesPageLimit)
		if err != nil {
			logger.Errorf("[Web] GetMemories error for user %s: %v", userID, err)
			errorMsg = "Failed to fetch memories: " + err.Error()
		} else {
			entries = make([]memoryDisplayEntry, 0, len(memories))
			for _, m := range memories {
				entries = append(entries, toDisplayEntry(m, cfg.Web.ContentTruncationLength))
			}
		}
	}

	pd := &memoriesPageData{
		UserID:   userID,
		Memories: entries,
		Count:    len(entries),
		Searched: searched,
		Error:    errorMsg,
	}

	renderStandardPage(w, r, ts, "memories", pd)
}

func handleMemoriesDelete(w http.ResponseWriter, r *http.Request, memStore memory.MemoryStore) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form data", http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(r.FormValue("userID"))
	memoryID := strings.TrimSpace(r.FormValue("memoryID"))

	if userID == "" || memoryID == "" {
		http.Error(w, "userID and memoryID are required", http.StatusBadRequest)
		return
	}

	record, err := memStore.GetMemory(ctx, memoryID)
	if err != nil {
		http.Error(w, "Memory not found", http.StatusNotFound)
		return
	}

	if record.UserID != userID {
		http.Error(w, "Ownership mismatch", http.StatusForbidden)
		return
	}

	if err := memStore.DeleteMemory(ctx, memoryID); err != nil {
		http.Error(w, "Failed to delete memory", http.StatusInternalServerError)
		return
	}

	setFlashCookie(w, "memories", "success", "Memory deleted successfully")
	http.Redirect(w, r, "/memories?userID="+userID, http.StatusSeeOther)
}

func toDisplayEntry(m *memory.Record, truncLen int) memoryDisplayEntry {
	content := m.Content
	if len(content) > truncLen {
		content = truncateToWord(content, truncLen)
	}

	createdAt := m.CreatedAt.Format(time.RFC3339)

	return memoryDisplayEntry{
		ID:         m.ID,
		UserID:     m.UserID,
		MemoryType: string(m.MemoryType),
		Content:    content,
		Summary:    m.Summary,
		Keywords:   m.Keywords,
		Confidence: m.Confidence,
		CreatedAt:  createdAt,
	}
}

func truncateToWord(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	cut := s[:maxLen]
	if idx := strings.LastIndexFunc(cut, unicode.IsSpace); idx > 0 {
		cut = cut[:idx]
	}
	return cut + "…"
}
