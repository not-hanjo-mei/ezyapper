package web

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"ezyapper/internal/config"
)

func ConfigHandler(cfgStore *atomic.Value, ts *TemplateSet, runtimeApplier RuntimeConfigApplier) http.HandlerFunc {
	var configMu sync.Mutex

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		switch r.Method {
		case http.MethodGet:
			cfg, ok := cfgStore.Load().(*config.Config)
			if !ok {
				http.Error(w, "Internal configuration error", http.StatusInternalServerError)
				return
			}
			data := cfg

			renderStandardPage(w, r, ts, "config", data)

		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				renderConfigError(w, r, ts, cfgStore, "Failed to parse form data")
				return
			}

			configMu.Lock()
			defer configMu.Unlock()

			oldCfg, ok := cfgStore.Load().(*config.Config)
			if !ok {
				http.Error(w, "Internal configuration error", http.StatusInternalServerError)
				return
			}
			newCfg := *oldCfg

			parseErrs := []string{}

			if v := r.FormValue("bot_name"); v != "" {
				newCfg.Discord.BotName = v
			}
			if v := r.FormValue("reply_percentage"); v != "" {
				pct, err := strconv.ParseFloat(v, 64)
				if err != nil {
					parseErrs = append(parseErrs, "reply_percentage must be a number")
				} else {
					newCfg.Discord.ReplyPercentage = pct / 100.0
				}
			}
			if v := r.FormValue("cooldown_seconds"); v != "" {
				sec, err := strconv.Atoi(v)
				if err != nil {
					parseErrs = append(parseErrs, "cooldown_seconds must be a whole number")
				} else {
					newCfg.Discord.CooldownSeconds = sec
				}
			}
			if v := r.FormValue("max_responses_per_minute"); v != "" {
				max, err := strconv.Atoi(v)
				if err != nil {
					parseErrs = append(parseErrs, "max_responses_per_minute must be a whole number")
				} else {
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
				tok, err := strconv.Atoi(v)
				if err != nil {
					parseErrs = append(parseErrs, "max_tokens must be a whole number")
				} else {
					newCfg.AI.MaxTokens = tok
				}
			}
			if v := r.FormValue("temperature"); v != "" {
				temp, err := strconv.ParseFloat(v, 32)
				if err != nil {
					parseErrs = append(parseErrs, "temperature must be a number")
				} else {
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
				max, err := strconv.Atoi(v)
				if err != nil {
					parseErrs = append(parseErrs, "vision_max_images must be a whole number")
				} else {
					newCfg.AI.Vision.MaxImages = max
				}
			}

			if v := r.FormValue("consolidation_interval"); v != "" {
				val, err := strconv.Atoi(v)
				if err != nil {
					parseErrs = append(parseErrs, "consolidation_interval must be a whole number")
				} else {
					newCfg.Memory.ConsolidationInterval = val
				}
			}
			if v := r.FormValue("short_term_limit"); v != "" {
				val, err := strconv.Atoi(v)
				if err != nil {
					parseErrs = append(parseErrs, "short_term_limit must be a whole number")
				} else {
					newCfg.Memory.ShortTermLimit = val
				}
			}
			if v := r.FormValue("retrieval_top_k"); v != "" {
				val, err := strconv.Atoi(v)
				if err != nil {
					parseErrs = append(parseErrs, "retrieval_top_k must be a whole number")
				} else {
					newCfg.Memory.Retrieval.TopK = val
				}
			}
			if v := r.FormValue("retrieval_min_score"); v != "" {
				val, err := strconv.ParseFloat(v, 64)
				if err != nil {
					parseErrs = append(parseErrs, "retrieval_min_score must be a number")
				} else {
					newCfg.Memory.Retrieval.MinScore = val
				}
			}

			if len(parseErrs) > 0 {
				renderConfigError(w, r, ts, cfgStore, "Failed to parse form values: "+strings.Join(parseErrs, "; "))
				return
			}

			// Validate FIRST — before any persistence
			if err := config.Validate(&newCfg); err != nil {
				http.Error(w, "Validation failed: "+err.Error(), http.StatusBadRequest)
				return
			}

			// Store new config first so ApplyRuntimeConfig reads the updated values
			cfgStore.Store(&newCfg)

			if runtimeApplier != nil {
				if err := runtimeApplier.ApplyRuntimeConfig(); err != nil {
					// Revert to old config on runtime apply failure
					cfgStore.Store(oldCfg)
					renderConfigError(w, r, ts, cfgStore, "Failed to apply runtime config: "+err.Error())
					return
				}
			}

			if err := newCfg.Save(); err != nil {
				cfgStore.Store(oldCfg)
				renderConfigError(w, r, ts, cfgStore, "Failed to save config: "+err.Error())
				return
			}

			setFlashCookie(w, "config", "success", "Settings saved successfully")
			http.Redirect(w, r, "/config", http.StatusSeeOther)

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func renderConfigError(w http.ResponseWriter, r *http.Request, ts *TemplateSet, cfgStore *atomic.Value, message string) {
	cfg, ok := cfgStore.Load().(*config.Config)
	if !ok {
		http.Error(w, "Internal configuration error", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	csrfToken := CSRFTokenFromContext(ctx)

	navItems := activeNavItems("config")

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
