package web

import (
	"net/http"
	"strings"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
)

// profileDisplayEntry is the view-model for a profile on the profiles page.
type profileDisplayEntry struct {
	UserID             string
	DisplayName        string
	Traits             []string
	Facts              map[string]string
	Preferences        map[string]string
	Interests          []string
	LastSummary        string
	PersonalitySummary string
	MessageCount       int
	MemoryCount        int
	FirstSeenAt        string
	LastActiveAt       string
	LastConsolidatedAt string
}

type profilesPageData struct {
	UserID   string
	Profile  *profileDisplayEntry
	Found    bool
	Searched bool
	EditMode bool
	Error    string
}

// ProfilesHandler returns an http.HandlerFunc for the user profiles page.
// GET  /profiles                — search form with no results
// GET  /profiles?userID=X       — view profile for user X
// GET  /profiles?userID=X&edit=true — edit profile display name
// POST /profiles/update         — update profile display name
func ProfilesHandler(profileStore memory.ProfileStore, ts *TemplateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case r.Method == http.MethodGet && path == "/profiles":
			handleProfilesGET(w, r, profileStore, ts)
		case r.Method == http.MethodPost && path == "/profiles/update":
			handleProfilesUpdate(w, r, profileStore)
		default:
			http.Error(w, "Not found", http.StatusNotFound)
		}
	}
}

func handleProfilesGET(w http.ResponseWriter, r *http.Request, profileStore memory.ProfileStore, ts *TemplateSet) {
	ctx := r.Context()
	userID := strings.TrimSpace(r.URL.Query().Get("userID"))
	editMode := r.URL.Query().Get("edit") == "true"
	pd := &profilesPageData{
		UserID:   userID,
		EditMode: editMode,
	}

	if userID != "" {
		pd.Searched = true
		profile, err := profileStore.GetProfile(ctx, userID)
		if err != nil {
			logger.Errorf("[web] GetProfile error for user %s: %v", userID, err)
			pd.Error = "Failed to fetch profile: " + err.Error()
		} else {
			pd.Found = true
			pd.Profile = toProfileDisplayEntry(profile)
		}
	}

	renderStandardPage(w, r, ts, "profiles", pd)
}

func handleProfilesUpdate(w http.ResponseWriter, r *http.Request, profileStore memory.ProfileStore) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form data", http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(r.FormValue("userID"))
	if userID == "" {
		http.Error(w, "userID is required", http.StatusBadRequest)
		return
	}

	displayName := strings.TrimSpace(r.FormValue("display_name"))

	ctx := r.Context()
	profile, err := profileStore.GetProfile(ctx, userID)
	if err != nil {
		setFlashCookie(w, "profiles", "error", "Profile not found for user: "+userID)
		http.Redirect(w, r, "/profiles?userID="+userID, http.StatusSeeOther)
		return
	}

	profile.DisplayName = displayName

	if err := profileStore.UpdateProfile(ctx, profile); err != nil {
		setFlashCookie(w, "profiles", "error", "Failed to update profile: "+err.Error())
		http.Redirect(w, r, "/profiles?userID="+userID+"&edit=true", http.StatusSeeOther)
		return
	}

	setFlashCookie(w, "profiles", "success", "Profile updated successfully")
	http.Redirect(w, r, "/profiles?userID="+userID, http.StatusSeeOther)
}

func toProfileDisplayEntry(p *memory.Profile) *profileDisplayEntry {
	entry := &profileDisplayEntry{
		UserID:             p.UserID,
		DisplayName:        p.DisplayName,
		Traits:             p.Traits,
		Facts:              p.Facts,
		Preferences:        p.Preferences,
		Interests:          p.Interests,
		LastSummary:        p.LastSummary,
		PersonalitySummary: p.PersonalitySummary,
		MessageCount:       p.MessageCount,
		MemoryCount:        p.MemoryCount,
	}

	if !p.FirstSeenAt.IsZero() {
		entry.FirstSeenAt = p.FirstSeenAt.Format(time.RFC3339)
	}
	if !p.LastActiveAt.IsZero() {
		entry.LastActiveAt = p.LastActiveAt.Format(time.RFC3339)
	}
	if !p.LastConsolidatedAt.IsZero() {
		entry.LastConsolidatedAt = p.LastConsolidatedAt.Format(time.RFC3339)
	}

	if entry.Traits == nil {
		entry.Traits = []string{}
	}
	if entry.Facts == nil {
		entry.Facts = map[string]string{}
	}
	if entry.Preferences == nil {
		entry.Preferences = map[string]string{}
	}
	if entry.Interests == nil {
		entry.Interests = []string{}
	}

	return entry
}
