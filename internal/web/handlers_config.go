package web

import (
	"net/http"

	"ezyapper/internal/config"

	"github.com/gin-gonic/gin"
)

func (s *Server) saveConfigOrError(c *gin.Context) bool {
	if err := s.config.Save(); err != nil {
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"discord": gin.H{
			"bot_name":         s.config.Discord.BotName,
			"reply_percentage": s.config.Discord.ReplyPercentage,
			"cooldown_seconds": s.config.Discord.CooldownSeconds,
		},
		"ai": gin.H{
			"model":         s.config.AI.Model,
			"vision_model":  s.config.AI.VisionModel,
			"vision_base64": s.config.AI.VisionBase64,
			"max_tokens":    s.config.AI.MaxTokens,
			"temperature":   s.config.AI.Temperature,
			"vision": gin.H{
				"mode":               s.config.AI.Vision.Mode,
				"description_prompt": s.config.AI.Vision.DescriptionPrompt,
				"max_images":         s.config.AI.Vision.MaxImages,
			},
		},
		"memory": gin.H{
			"consolidation_interval": s.config.Memory.ConsolidationInterval,
			"short_term_limit":       s.config.Memory.ShortTermLimit,
			"retrieval": gin.H{
				"top_k":     s.config.Memory.Retrieval.TopK,
				"min_score": s.config.Memory.Retrieval.MinScore,
			},
		},
		"web": gin.H{
			"port": s.config.Web.Port,
		},
	})
}

func (s *Server) updateConfig(c *gin.Context) {
	var updates map[string]any
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	previous := *s.config

	updatedSections := []string{}

	if discordUpdates, ok := updates["discord"].(map[string]any); ok {
		if name, ok := discordUpdates["bot_name"].(string); ok && name != "" {
			s.config.Discord.BotName = name
		}
		if replyPct, ok := discordUpdates["reply_percentage"].(float64); ok {
			s.config.Discord.ReplyPercentage = replyPct
		}
		if cooldown, ok := discordUpdates["cooldown_seconds"].(float64); ok {
			s.config.Discord.CooldownSeconds = int(cooldown)
		}
		if maxResp, ok := discordUpdates["max_responses_per_minute"].(float64); ok {
			s.config.Discord.MaxResponsesPerMin = int(maxResp)
		}
		updatedSections = append(updatedSections, "discord")
	}

	if aiUpdates, ok := updates["ai"].(map[string]any); ok {
		if model, ok := aiUpdates["model"].(string); ok && model != "" {
			s.config.AI.Model = model
		}
		if visionModel, ok := aiUpdates["vision_model"].(string); ok && visionModel != "" {
			s.config.AI.VisionModel = visionModel
		}
		if visionBase64, ok := aiUpdates["vision_base64"].(bool); ok {
			s.config.AI.VisionBase64 = visionBase64
		}
		if maxTokens, ok := aiUpdates["max_tokens"].(float64); ok {
			s.config.AI.MaxTokens = int(maxTokens)
		}
		if temp, ok := aiUpdates["temperature"].(float64); ok {
			s.config.AI.Temperature = float32(temp)
		}

		if visionUpdates, ok := aiUpdates["vision"].(map[string]any); ok {
			if mode, ok := visionUpdates["mode"].(string); ok && mode != "" {
				s.config.AI.Vision.Mode = config.VisionMode(mode)
			}
			if descPrompt, ok := visionUpdates["description_prompt"].(string); ok {
				s.config.AI.Vision.DescriptionPrompt = descPrompt
			}
			if maxImages, ok := visionUpdates["max_images"].(float64); ok {
				s.config.AI.Vision.MaxImages = int(maxImages)
			}
		}

		if prompt, ok := aiUpdates["system_prompt"].(string); ok {
			s.config.AI.SystemPrompt = prompt
		}
		updatedSections = append(updatedSections, "ai")
	}

	if memoryUpdates, ok := updates["memory"].(map[string]any); ok {
		if interval, ok := memoryUpdates["consolidation_interval"].(float64); ok {
			s.config.Memory.ConsolidationInterval = int(interval)
		}
		if limit, ok := memoryUpdates["short_term_limit"].(float64); ok {
			s.config.Memory.ShortTermLimit = int(limit)
		}
		updatedSections = append(updatedSections, "memory")
	}

	if err := config.Validate(s.config); err != nil {
		*s.config = previous
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config update: " + err.Error()})
		return
	}

	if !s.applyRuntimeConfigOrError(c) {
		*s.config = previous
		return
	}

	if !s.saveConfigOrError(c) {
		*s.config = previous
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":          "config updated",
		"updated_sections": updatedSections,
	})
}

func (s *Server) getDiscordConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"bot_name":                 s.config.Discord.BotName,
		"reply_percentage":         s.config.Discord.ReplyPercentage,
		"cooldown_seconds":         s.config.Discord.CooldownSeconds,
		"max_responses_per_minute": s.config.Discord.MaxResponsesPerMin,
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

	s.mu.Lock()
	defer s.mu.Unlock()

	previous := *s.config

	if updates.BotName != "" {
		s.config.Discord.BotName = updates.BotName
	}
	if updates.ReplyPercentage != nil {
		s.config.Discord.ReplyPercentage = *updates.ReplyPercentage
	}
	if updates.CooldownSeconds != nil {
		s.config.Discord.CooldownSeconds = *updates.CooldownSeconds
	}
	if updates.MaxResponsesPerMin != nil {
		s.config.Discord.MaxResponsesPerMin = *updates.MaxResponsesPerMin
	}

	if err := config.Validate(s.config); err != nil {
		*s.config = previous
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config update: " + err.Error()})
		return
	}

	if !s.applyRuntimeConfigOrError(c) {
		*s.config = previous
		return
	}

	if !s.saveConfigOrError(c) {
		*s.config = previous
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "discord config updated",
		"config":  s.config.Discord,
	})
}

func (s *Server) getAIConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"model":         s.config.AI.Model,
		"vision_model":  s.config.AI.VisionModel,
		"max_tokens":    s.config.AI.MaxTokens,
		"temperature":   s.config.AI.Temperature,
		"system_prompt": s.config.AI.SystemPrompt,
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

	s.mu.Lock()
	defer s.mu.Unlock()

	previous := *s.config

	if updates.Model != "" {
		s.config.AI.Model = updates.Model
	}
	if updates.VisionModel != "" {
		s.config.AI.VisionModel = updates.VisionModel
	}
	if updates.MaxTokens != nil {
		s.config.AI.MaxTokens = *updates.MaxTokens
	}
	if updates.Temperature != nil {
		s.config.AI.Temperature = *updates.Temperature
	}
	if updates.SystemPrompt != "" {
		s.config.AI.SystemPrompt = updates.SystemPrompt
	}

	if err := config.Validate(s.config); err != nil {
		*s.config = previous
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config update: " + err.Error()})
		return
	}

	if !s.applyRuntimeConfigOrError(c) {
		*s.config = previous
		return
	}

	if !s.saveConfigOrError(c) {
		*s.config = previous
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "ai config updated",
		"config":  s.config.AI,
	})
}
