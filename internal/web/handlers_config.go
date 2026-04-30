package web

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"sync/atomic"

	"ezyapper/internal/config"
)

func ConfigHandler(cfgStore *atomic.Value, ts *TemplateSet, runtimeApplier RuntimeConfigApplier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		switch r.Method {
		case http.MethodGet:
			cfg := cfgStore.Load().(*config.Config)
			data := cfg
			csrfToken := CSRFTokenFromContext(ctx)
			flash := flashFromCookie(r)

			navItems := []NavItem{
				{Label: "Dashboard", Href: "/", Icon: "dashboard"},
				{Label: "Configuration", Href: "/config", Icon: "settings", Active: true},
				{Label: "Memories", Href: "/memories", Icon: "memory"},
				{Label: "Profiles", Href: "/profiles", Icon: "person"},
				{Label: "Channels", Href: "/channels", Icon: "forum"},
				{Label: "Plugins", Href: "/plugins", Icon: "extension"},
				{Label: "Logs", Href: "/logs", Icon: "description"},
			}

			RenderPage(w, ts, "config", &PageData{
				Title:     "Configuration",
				ActiveNav: "config",
				CSRFToken: csrfToken,
				Flash:     flash,
				Data:      data,
				NavItems:  navItems,
			})

		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				renderConfigError(w, r, ts, cfgStore, "Failed to parse form data")
				return
			}

			oldCfg := cfgStore.Load().(*config.Config)
			newCfg := *oldCfg

			if v := r.FormValue("bot_name"); v != "" {
				newCfg.Discord.BotName = v
			}
			if v := r.FormValue("reply_percentage"); v != "" {
				if pct, err := strconv.ParseFloat(v, 64); err == nil {
					newCfg.Discord.ReplyPercentage = pct / 100.0
				}
			}
			if v := r.FormValue("cooldown_seconds"); v != "" {
				if sec, err := strconv.Atoi(v); err == nil {
					newCfg.Discord.CooldownSeconds = sec
				}
			}
			if v := r.FormValue("max_responses_per_minute"); v != "" {
				if max, err := strconv.Atoi(v); err == nil {
					newCfg.Discord.MaxResponsesPerMin = max
				}
			}

			if v := r.FormValue("model"); v != "" {
				newCfg.AI.Model = v
			}
			if v := r.FormValue("vision_model"); v != "" {
				newCfg.AI.VisionModel = v
			}
			if v := r.FormValue("max_tokens"); v != "" {
				if tok, err := strconv.Atoi(v); err == nil {
					newCfg.AI.MaxTokens = tok
				}
			}
			if v := r.FormValue("temperature"); v != "" {
				if temp, err := strconv.ParseFloat(v, 32); err == nil {
					newCfg.AI.Temperature = float32(temp)
				}
			}
			if v := r.FormValue("system_prompt"); v != "" {
				newCfg.AI.SystemPrompt = v
			}

			if v := r.FormValue("vision_mode"); v != "" {
				newCfg.AI.Vision.Mode = config.VisionMode(v)
			}
			if v := r.FormValue("vision_description_prompt"); v != "" {
				newCfg.AI.Vision.DescriptionPrompt = v
			}
			if v := r.FormValue("vision_max_images"); v != "" {
				if max, err := strconv.Atoi(v); err == nil {
					newCfg.AI.Vision.MaxImages = max
				}
			}

			if v := r.FormValue("consolidation_interval"); v != "" {
				if val, err := strconv.Atoi(v); err == nil {
					newCfg.Memory.ConsolidationInterval = val
				}
			}
			if v := r.FormValue("short_term_limit"); v != "" {
				if val, err := strconv.Atoi(v); err == nil {
					newCfg.Memory.ShortTermLimit = val
				}
			}
			if v := r.FormValue("retrieval_top_k"); v != "" {
				if val, err := strconv.Atoi(v); err == nil {
					newCfg.Memory.Retrieval.TopK = val
				}
			}
			if v := r.FormValue("retrieval_min_score"); v != "" {
				if val, err := strconv.ParseFloat(v, 64); err == nil {
					newCfg.Memory.Retrieval.MinScore = val
				}
			}

			// Validate FIRST — before any persistence
			if err := config.Validate(&newCfg); err != nil {
				http.Error(w, "Validation failed: "+err.Error(), http.StatusBadRequest)
				return
			}

			// Save to YAML AFTER validation passes
			if err := newCfg.Save(); err != nil {
				http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
				return
			}

			cfgStore.Store(&newCfg)

			flashMsg := "Settings saved successfully"
			flashType := "success"
			if runtimeApplier != nil {
				if err := runtimeApplier.ApplyRuntimeConfig(); err != nil {
					cfgStore.Store(oldCfg) // rollback: restore previous config in memory
					flashMsg = "Settings saved but runtime apply failed"
					flashType = "warning"
				}
			}

			setFlashCookie(w, flashType, flashMsg)
			http.Redirect(w, r, "/config", http.StatusSeeOther)

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func renderConfigError(w http.ResponseWriter, r *http.Request, ts *TemplateSet, cfgStore *atomic.Value, message string) {
	cfg := cfgStore.Load().(*config.Config)
	ctx := r.Context()
	csrfToken := CSRFTokenFromContext(ctx)

	navItems := []NavItem{
		{Label: "Dashboard", Href: "/", Icon: "dashboard"},
		{Label: "Configuration", Href: "/config", Icon: "settings", Active: true},
		{Label: "Memories", Href: "/memories", Icon: "memory"},
		{Label: "Profiles", Href: "/profiles", Icon: "person"},
		{Label: "Channels", Href: "/channels", Icon: "forum"},
		{Label: "Plugins", Href: "/plugins", Icon: "extension"},
		{Label: "Logs", Href: "/logs", Icon: "description"},
	}

	RenderPage(w, ts, "config", &PageData{
		Title:     "Configuration",
		ActiveNav: "config",
		CSRFToken: csrfToken,
		Flash: &FlashMessage{
			Type:    "error",
			Message: message,
		},
		Data:     cfg,
		NavItems: navItems,
	})
}

func setFlashCookie(w http.ResponseWriter, flashType, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "flash_type",
		Value:    flashType,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   60,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "flash_msg",
		Value:    base64.URLEncoding.EncodeToString([]byte(message)),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   60,
	})
}

func flashFromCookie(r *http.Request) *FlashMessage {
	typeCookie, err := r.Cookie("flash_type")
	if err != nil {
		return nil
	}
	msgCookie, err := r.Cookie("flash_msg")
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
