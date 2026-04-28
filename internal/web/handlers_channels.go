package web

import (
	"encoding/base64"
	"net/http"
	"slices"
	"sync/atomic"

	"ezyapper/internal/config"
)

type channelEntry struct {
	ID   string
	Name string
	Type string
}

type channelList struct {
	Users    []channelEntry
	Channels []channelEntry
	Guilds   []channelEntry
}

type channelsPageData struct {
	Blacklist channelList
	Whitelist channelList
}

func validChannelType(t string) bool {
	return t == "user" || t == "channel" || t == "guild"
}

func ChannelsHandler(cfgStore *atomic.Value, discordInfo DiscordInfoProvider, ts *TemplateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case r.Method == http.MethodGet && path == "/channels":
			handleChannelsGET(w, r, cfgStore, discordInfo, ts)

		case r.Method == http.MethodPost && path == "/channels/blacklist/add":
			handleChannelsPOST(w, r, cfgStore, ts, "blacklist", true)

		case r.Method == http.MethodPost && path == "/channels/blacklist/remove":
			handleChannelsPOST(w, r, cfgStore, ts, "blacklist", false)

		case r.Method == http.MethodPost && path == "/channels/whitelist/add":
			handleChannelsPOST(w, r, cfgStore, ts, "whitelist", true)

		case r.Method == http.MethodPost && path == "/channels/whitelist/remove":
			handleChannelsPOST(w, r, cfgStore, ts, "whitelist", false)

		default:
			http.Error(w, "Not found", http.StatusNotFound)
		}
	}
}

func handleChannelsGET(w http.ResponseWriter, r *http.Request, cfgStore *atomic.Value, info DiscordInfoProvider, ts *TemplateSet) {
	cfg := cfgStore.Load().(*config.Config)
	ctx := r.Context()
	csrfToken := CSRFTokenFromContext(ctx)
	flash := flashFromCookieChannels(r)

	pageData := buildChannelsPageData(cfg, info, csrfToken)
	pageData.Flash = flash
	RenderPage(w, ts, "channels", pageData)
}

func buildChannelsPageData(cfg *config.Config, info DiscordInfoProvider, csrfToken string) *PageData {
	pd := &channelsPageData{}

	pd.Blacklist.Users = buildEntries(cfg.Blacklist.Users, "user", info)
	pd.Blacklist.Channels = buildEntries(cfg.Blacklist.Channels, "channel", info)
	pd.Blacklist.Guilds = buildEntries(cfg.Blacklist.Guilds, "guild", info)

	pd.Whitelist.Users = buildEntries(cfg.Whitelist.Users, "user", info)
	pd.Whitelist.Channels = buildEntries(cfg.Whitelist.Channels, "channel", info)
	pd.Whitelist.Guilds = buildEntries(cfg.Whitelist.Guilds, "guild", info)

	navItems := []NavItem{
		{Label: "Dashboard", Href: "/", Icon: "dashboard"},
		{Label: "Configuration", Href: "/config", Icon: "settings"},
		{Label: "Memories", Href: "/memories", Icon: "memory"},
		{Label: "Profiles", Href: "/profiles", Icon: "person"},
		{Label: "Channels", Href: "/channels", Icon: "forum", Active: true},
		{Label: "Plugins", Href: "/plugins", Icon: "extension"},
		{Label: "Logs", Href: "/logs", Icon: "description"},
	}

	return &PageData{
		Title:        "Channels",
		ActiveNav:    "channels",
		CSRFToken:    csrfToken,
		Data:         pd,
		NavItems:  navItems,
	}
}

func buildEntries(ids []string, entryType string, info DiscordInfoProvider) []channelEntry {
	entries := make([]channelEntry, 0, len(ids))
	for _, id := range ids {
		name := resolveName(id, entryType, info)
		entries = append(entries, channelEntry{
			ID:   id,
			Name: name,
			Type: entryType,
		})
	}
	return entries
}

func resolveName(id, entryType string, info DiscordInfoProvider) string {
	if info == nil {
		return id
	}
	switch entryType {
	case "user":
		return info.GetUserName("", id)
	case "channel":
		return info.GetChannelName(id)
	case "guild":
		return info.GetGuildName(id)
	default:
		return id
	}
}

func handleChannelsPOST(w http.ResponseWriter, r *http.Request, cfgStore *atomic.Value, ts *TemplateSet, list string, add bool) {
	if err := r.ParseForm(); err != nil {
		renderChannelsError(w, r, cfgStore, ts, "Failed to parse form data")
		return
	}

	entryType := r.FormValue("type")
	entryID := r.FormValue("id")

	if !validChannelType(entryType) {
		renderChannelsError(w, r, cfgStore, ts, "Invalid type: must be 'user', 'channel', or 'guild'")
		return
	}
	if entryID == "" {
		renderChannelsError(w, r, cfgStore, ts, "ID is required")
		return
	}

	oldCfg := cfgStore.Load().(*config.Config)
	newCfg := *oldCfg

	var target *[]string
	if list == "blacklist" {
		switch entryType {
		case "user":
			target = &newCfg.Blacklist.Users
		case "channel":
			target = &newCfg.Blacklist.Channels
		case "guild":
			target = &newCfg.Blacklist.Guilds
		}
	} else {
		switch entryType {
		case "user":
			target = &newCfg.Whitelist.Users
		case "channel":
			target = &newCfg.Whitelist.Channels
		case "guild":
			target = &newCfg.Whitelist.Guilds
		}
	}

	if add {
		if slices.Contains(*target, entryID) {
			renderChannelsError(w, r, cfgStore, ts, "Entry already exists")
			return
		}
		*target = append(*target, entryID)
	} else {
		idx := slices.Index(*target, entryID)
		if idx == -1 {
			renderChannelsError(w, r, cfgStore, ts, "Entry not found")
			return
		}
		*target = append((*target)[:idx], (*target)[idx+1:]...)
	}

	if err := newCfg.Save(); err != nil {
		renderChannelsError(w, r, cfgStore, ts, "Failed to save config: "+err.Error())
		return
	}

	cfgStore.Store(&newCfg)

	action := "added to"
	if !add {
		action = "removed from"
	}
	setFlashCookieChannels(w, "success", "Entry "+action+" "+list)
	http.Redirect(w, r, "/channels", http.StatusSeeOther)
}

func renderChannelsError(w http.ResponseWriter, r *http.Request, cfgStore *atomic.Value, ts *TemplateSet, message string) {
	cfg := cfgStore.Load().(*config.Config)
	ctx := r.Context()
	csrfToken := CSRFTokenFromContext(ctx)

	pageData := buildChannelsPageData(cfg, nil, csrfToken)
	pageData.Flash = &FlashMessage{
		Type:    "error",
		Message: message,
	}
	RenderPage(w, ts, "channels", pageData)
}

func setFlashCookieChannels(w http.ResponseWriter, flashType, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "channels_flash_type",
		Value:    flashType,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   60,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "channels_flash_msg",
		Value:    base64.URLEncoding.EncodeToString([]byte(message)),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   60,
	})
}

func flashFromCookieChannels(r *http.Request) *FlashMessage {
	typeCookie, err := r.Cookie("channels_flash_type")
	if err != nil {
		return nil
	}
	msgCookie, err := r.Cookie("channels_flash_msg")
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
