package web

import (
	"net/http"

	"ezyapper/internal/config"

	"github.com/gin-gonic/gin"
)

func (s *Server) saveConfigOrError(c *gin.Context, cfg *config.Config) bool {
	if err := cfg.Save(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist config: " + err.Error()})
		return false
	}

	return true
}

func (s *Server) applyRuntimeConfigOrError(c *gin.Context) bool {
	updater, ok := s.discordFetcher.(RuntimeConfigApplier)
	if !ok || updater == nil {
		return true
	}

	if err := updater.ApplyRuntimeConfig(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to apply runtime config: " + err.Error()})
		return false
	}

	return true
}

func (s *Server) getConfig(c *gin.Context) {
	cfg := s.cfg()
	c.JSON(http.StatusOK, gin.H{
		"discord": gin.H{
			"bot_name":         cfg.Discord.BotName,
			"reply_percentage": cfg.Discord.ReplyPercentage,
			"cooldown_seconds": cfg.Discord.CooldownSeconds,
		},
		"ai": gin.H{
			"model":         cfg.AI.Model,
			"vision_model":  cfg.AI.VisionModel,
			"vision_base64": cfg.AI.VisionBase64,
			"max_tokens":    cfg.AI.MaxTokens,
			"temperature":   cfg.AI.Temperature,
			"vision": gin.H{
				"mode":               cfg.AI.Vision.Mode,
				"description_prompt": cfg.AI.Vision.DescriptionPrompt,
				"max_images":         cfg.AI.Vision.MaxImages,
			},
		},
		"memory": gin.H{
			"consolidation_interval": cfg.Memory.ConsolidationInterval,
			"short_term_limit":       cfg.Memory.ShortTermLimit,
			"retrieval": gin.H{
				"top_k":     cfg.Memory.Retrieval.TopK,
				"min_score": cfg.Memory.Retrieval.MinScore,
			},
		},
		"web": gin.H{
			"port": cfg.Web.Port,
		},
	})
}

func (s *Server) updateConfig(c *gin.Context) {
	var updates map[string]any
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	oldCfg := s.cfg()
	newCfg := *oldCfg

	updatedSections := []string{}

	if discordUpdates, ok := updates["discord"].(map[string]any); ok {
		if name, ok := discordUpdates["bot_name"].(string); ok && name != "" {
			newCfg.Discord.BotName = name
		}
		if replyPct, ok := discordUpdates["reply_percentage"].(float64); ok {
			newCfg.Discord.ReplyPercentage = replyPct
		}
		if cooldown, ok := discordUpdates["cooldown_seconds"].(float64); ok {
			newCfg.Discord.CooldownSeconds = int(cooldown)
		}
		if maxResp, ok := discordUpdates["max_responses_per_minute"].(float64); ok {
			newCfg.Discord.MaxResponsesPerMin = int(maxResp)
		}
		updatedSections = append(updatedSections, "discord")
	}

	if aiUpdates, ok := updates["ai"].(map[string]any); ok {
		if model, ok := aiUpdates["model"].(string); ok && model != "" {
			newCfg.AI.Model = model
		}
		if visionModel, ok := aiUpdates["vision_model"].(string); ok && visionModel != "" {
			newCfg.AI.VisionModel = visionModel
		}
		if visionBase64, ok := aiUpdates["vision_base64"].(bool); ok {
			newCfg.AI.VisionBase64 = visionBase64
		}
		if maxTokens, ok := aiUpdates["max_tokens"].(float64); ok {
			newCfg.AI.MaxTokens = int(maxTokens)
		}
		if temp, ok := aiUpdates["temperature"].(float64); ok {
			newCfg.AI.Temperature = float32(temp)
		}

		if visionUpdates, ok := aiUpdates["vision"].(map[string]any); ok {
			if mode, ok := visionUpdates["mode"].(string); ok && mode != "" {
				newCfg.AI.Vision.Mode = config.VisionMode(mode)
			}
			if descPrompt, ok := visionUpdates["description_prompt"].(string); ok {
				newCfg.AI.Vision.DescriptionPrompt = descPrompt
			}
			if maxImages, ok := visionUpdates["max_images"].(float64); ok {
				newCfg.AI.Vision.MaxImages = int(maxImages)
			}
		}

		if prompt, ok := aiUpdates["system_prompt"].(string); ok {
			newCfg.AI.SystemPrompt = prompt
		}
		updatedSections = append(updatedSections, "ai")
	}

	if memoryUpdates, ok := updates["memory"].(map[string]any); ok {
		if interval, ok := memoryUpdates["consolidation_interval"].(float64); ok {
			newCfg.Memory.ConsolidationInterval = int(interval)
		}
		if limit, ok := memoryUpdates["short_term_limit"].(float64); ok {
			newCfg.Memory.ShortTermLimit = int(limit)
		}
		updatedSections = append(updatedSections, "memory")
	}

	if err := config.Validate(&newCfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config update: " + err.Error()})
		return
	}

	s.configStore.Store(&newCfg)

	if !s.applyRuntimeConfigOrError(c) {
		s.configStore.Store(oldCfg)
		return
	}

	if !s.saveConfigOrError(c, &newCfg) {
		s.configStore.Store(oldCfg)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":          "config updated",
		"updated_sections": updatedSections,
	})
}

func (s *Server) getDiscordConfig(c *gin.Context) {
	cfg := s.cfg()
	c.JSON(http.StatusOK, gin.H{
		"bot_name":                 cfg.Discord.BotName,
		"reply_percentage":         cfg.Discord.ReplyPercentage,
		"cooldown_seconds":         cfg.Discord.CooldownSeconds,
		"max_responses_per_minute": cfg.Discord.MaxResponsesPerMin,
	})
}

func (s *Server) updateDiscordConfig(c *gin.Context) {
	var updates struct {
		BotName            string   `json:"bot_name"`
		ReplyPercentage    *float64 `json:"reply_percentage"`
		CooldownSeconds    *int     `json:"cooldown_seconds"`
		MaxResponsesPerMin *int     `json:"max_responses_per_minute"`
	}

	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	oldCfg := s.cfg()
	newCfg := *oldCfg

	if updates.BotName != "" {
		newCfg.Discord.BotName = updates.BotName
	}
	if updates.ReplyPercentage != nil {
		newCfg.Discord.ReplyPercentage = *updates.ReplyPercentage
	}
	if updates.CooldownSeconds != nil {
		newCfg.Discord.CooldownSeconds = *updates.CooldownSeconds
	}
	if updates.MaxResponsesPerMin != nil {
		newCfg.Discord.MaxResponsesPerMin = *updates.MaxResponsesPerMin
	}

	if err := config.Validate(&newCfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config update: " + err.Error()})
		return
	}

	s.configStore.Store(&newCfg)

	if !s.applyRuntimeConfigOrError(c) {
		s.configStore.Store(oldCfg)
		return
	}

	if !s.saveConfigOrError(c, &newCfg) {
		s.configStore.Store(oldCfg)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "discord config updated",
		"config":  newCfg.Discord,
	})
}

func (s *Server) getAIConfig(c *gin.Context) {
	cfg := s.cfg()
	c.JSON(http.StatusOK, gin.H{
		"model":         cfg.AI.Model,
		"vision_model":  cfg.AI.VisionModel,
		"max_tokens":    cfg.AI.MaxTokens,
		"temperature":   cfg.AI.Temperature,
		"system_prompt": cfg.AI.SystemPrompt,
	})
}

func (s *Server) updateAIConfig(c *gin.Context) {
	var updates struct {
		Model        string   `json:"model"`
		VisionModel  string   `json:"vision_model"`
		MaxTokens    *int     `json:"max_tokens"`
		Temperature  *float32 `json:"temperature"`
		SystemPrompt string   `json:"system_prompt"`
	}

	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	oldCfg := s.cfg()
	newCfg := *oldCfg

	if updates.Model != "" {
		newCfg.AI.Model = updates.Model
	}
	if updates.VisionModel != "" {
		newCfg.AI.VisionModel = updates.VisionModel
	}
	if updates.MaxTokens != nil {
		newCfg.AI.MaxTokens = *updates.MaxTokens
	}
	if updates.Temperature != nil {
		newCfg.AI.Temperature = *updates.Temperature
	}
	if updates.SystemPrompt != "" {
		newCfg.AI.SystemPrompt = updates.SystemPrompt
	}

	if err := config.Validate(&newCfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config update: " + err.Error()})
		return
	}

	s.configStore.Store(&newCfg)

	if !s.applyRuntimeConfigOrError(c) {
		s.configStore.Store(oldCfg)
		return
	}

	if !s.saveConfigOrError(c, &newCfg) {
		s.configStore.Store(oldCfg)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "ai config updated",
		"config":  newCfg.AI,
	})
}
