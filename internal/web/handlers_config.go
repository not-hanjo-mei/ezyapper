package web

import (
	"fmt"
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

			parseErrs := applyConfigForm(r, &newCfg)

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

type configFieldApplier struct {
	formKey string
	apply   func(cfg *config.Config, raw string) error
}

// applyConfigForm runs each applier in declared order and returns the
// collected parse errors. The order is a behavior contract: tests assert on
// combined messages built via strings.Join(parseErrs, "; ").
func applyConfigForm(r *http.Request, cfg *config.Config) []string {
	var parseErrs []string
	for _, applier := range configFormAppliers {
		v := r.FormValue(applier.formKey)
		if v == "" {
			continue
		}
		if err := applier.apply(cfg, v); err != nil {
			parseErrs = append(parseErrs, err.Error())
		}
	}
	return parseErrs
}

var configFormAppliers = []configFieldApplier{
	{formKey: "bot_name", apply: func(c *config.Config, v string) error { c.Discord.BotName = v; return nil }},
	{formKey: "reply_percentage", apply: func(c *config.Config, v string) error {
		pct, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("reply_percentage must be a number")
		}
		c.Discord.ReplyPercentage = pct / 100.0
		return nil
	}},
	{formKey: "cooldown_seconds", apply: func(c *config.Config, v string) error {
		sec, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("cooldown_seconds must be a whole number")
		}
		c.Discord.CooldownSeconds = sec
		return nil
	}},
	{formKey: "max_responses_per_minute", apply: func(c *config.Config, v string) error {
		max, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("max_responses_per_minute must be a whole number")
		}
		c.Discord.MaxResponsesPerMin = max
		return nil
	}},
	{formKey: "model", apply: func(c *config.Config, v string) error { c.AI.Model = v; return nil }},
	{formKey: "vision_model", apply: func(c *config.Config, v string) error { c.AI.Vision.Model = v; return nil }},
	{formKey: "max_tokens", apply: func(c *config.Config, v string) error {
		tok, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("max_tokens must be a whole number")
		}
		c.AI.MaxTokens = tok
		return nil
	}},
	{formKey: "temperature", apply: func(c *config.Config, v string) error {
		// Keep bit-size 32: ParseFloat(v, 32) then cast to float32.
		temp, err := strconv.ParseFloat(v, 32)
		if err != nil {
			return fmt.Errorf("temperature must be a number")
		}
		c.AI.Temperature = float32(temp)
		return nil
	}},
	{formKey: "system_prompt", apply: func(c *config.Config, v string) error { c.AI.SystemPrompt = v; return nil }},
	{formKey: "vision_mode", apply: func(c *config.Config, v string) error { c.AI.Vision.Mode = config.VisionMode(v); return nil }},
	{formKey: "vision_description_prompt", apply: func(c *config.Config, v string) error { c.AI.Vision.DescriptionPrompt = v; return nil }},
	{formKey: "consolidation_interval", apply: func(c *config.Config, v string) error {
		val, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("consolidation_interval must be a whole number")
		}
		c.Memory.ConsolidationInterval = val
		return nil
	}},
	{formKey: "short_term_limit", apply: func(c *config.Config, v string) error {
		val, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("short_term_limit must be a whole number")
		}
		c.Memory.ShortTermLimit = val
		return nil
	}},
	{formKey: "retrieval_top_k", apply: func(c *config.Config, v string) error {
		val, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("retrieval_top_k must be a whole number")
		}
		c.Memory.Retrieval.TopK = val
		return nil
	}},
	{formKey: "retrieval_min_score", apply: func(c *config.Config, v string) error {
		val, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("retrieval_min_score must be a number")
		}
		c.Memory.Retrieval.MinScore = val
		return nil
	}},
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
