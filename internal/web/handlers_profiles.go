package web

import (
	"encoding/base64"
	"net/http"
	"strings"
	"time"

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
	csrfToken := CSRFTokenFromContext(ctx)
	flash := flashFromCookieProfiles(r)

	pd := &profilesPageData{
		UserID:   userID,
		EditMode: editMode,
	}

	if userID != "" {
		pd.Searched = true
		profile, err := profileStore.GetProfile(ctx, userID)
		if err == nil {
			pd.Found = true
			pd.Profile = toProfileDisplayEntry(profile)
		}
	}

	navItems := []NavItem{
		{Label: "Dashboard", Href: "/", Icon: "dashboard"},
		{Label: "Configuration", Href: "/config", Icon: "settings"},
		{Label: "Memories", Href: "/memories", Icon: "memory"},
		{Label: "Profiles", Href: "/profiles", Icon: "person", Active: true},
		{Label: "Channels", Href: "/channels", Icon: "forum"},
		{Label: "Plugins", Href: "/plugins", Icon: "extension"},
		{Label: "Logs", Href: "/logs", Icon: "description"},
	}

	RenderPage(w, ts, "profiles", &PageData{
		Title:     "User Profiles",
		ActiveNav: "profiles",
		CSRFToken: csrfToken,
		Flash:     flash,
		Data:      pd,
		NavItems:  navItems,
	})
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
		setFlashCookieProfiles(w, "error", "Profile not found for user: "+userID)
		http.Redirect(w, r, "/profiles?userID="+userID, http.StatusSeeOther)
		return
	}

	profile.DisplayName = displayName

	if err := profileStore.UpdateProfile(ctx, profile); err != nil {
		setFlashCookieProfiles(w, "error", "Failed to update profile: "+err.Error())
		http.Redirect(w, r, "/profiles?userID="+userID+"&edit=true", http.StatusSeeOther)
		return
	}

	setFlashCookieProfiles(w, "success", "Profile updated successfully")
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

func setFlashCookieProfiles(w http.ResponseWriter, flashType, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "profiles_flash_type",
		Value:    flashType,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   60,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "profiles_flash_msg",
		Value:    base64.URLEncoding.EncodeToString([]byte(message)),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   60,
	})
}

func flashFromCookieProfiles(r *http.Request) *FlashMessage {
	typeCookie, err := r.Cookie("profiles_flash_type")
	if err != nil {
		return nil
	}
	msgCookie, err := r.Cookie("profiles_flash_msg")
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
