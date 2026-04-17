package web

import (
	"net/http"
	"strings"

	"ezyapper/internal/config"
	"ezyapper/internal/utils"

	"github.com/gin-gonic/gin"
)

type listConfig struct {
	users    *[]string
	channels *[]string
	guilds   *[]string
}

type listEntryRequest struct {
	Type string `json:"type" binding:"required"`
	ID   string `json:"id" binding:"required"`
}

func (s *Server) getBlacklist(c *gin.Context) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"users":    s.config.Blacklist.Users,
		"channels": s.config.Blacklist.Channels,
		"guilds":   s.config.Blacklist.Guilds,
	})
}

func (s *Server) addToBlacklist(c *gin.Context) {
	s.addToList(c, "blacklist", listConfig{
		users:    &s.config.Blacklist.Users,
		channels: &s.config.Blacklist.Channels,
		guilds:   &s.config.Blacklist.Guilds,
	})
}

func (s *Server) removeFromBlacklist(c *gin.Context) {
	s.removeFromList(c, "blacklist", listConfig{
		users:    &s.config.Blacklist.Users,
		channels: &s.config.Blacklist.Channels,
		guilds:   &s.config.Blacklist.Guilds,
	})
}

func (s *Server) getWhitelist(c *gin.Context) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"users":    s.config.Whitelist.Users,
		"channels": s.config.Whitelist.Channels,
		"guilds":   s.config.Whitelist.Guilds,
	})
}

func (s *Server) addToWhitelist(c *gin.Context) {
	s.addToList(c, "whitelist", listConfig{
		users:    &s.config.Whitelist.Users,
		channels: &s.config.Whitelist.Channels,
		guilds:   &s.config.Whitelist.Guilds,
	})
}

func (s *Server) removeFromWhitelist(c *gin.Context) {
	s.removeFromList(c, "whitelist", listConfig{
		users:    &s.config.Whitelist.Users,
		channels: &s.config.Whitelist.Channels,
		guilds:   &s.config.Whitelist.Guilds,
	})
}

func (s *Server) addToList(c *gin.Context, listName string, cfg listConfig) {
	var req listEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	entryType := strings.ToLower(strings.TrimSpace(req.Type))
	entryID := strings.TrimSpace(req.ID)

	s.mu.Lock()
	defer s.mu.Unlock()

	previous := *s.config

	switch entryType {
	case "user":
		if utils.Contains(*cfg.users, entryID) {
			c.JSON(http.StatusOK, gin.H{"message": "already in " + listName})
			return
		}
		*cfg.users = append(*cfg.users, entryID)
	case "channel":
		if utils.Contains(*cfg.channels, entryID) {
			c.JSON(http.StatusOK, gin.H{"message": "already in " + listName})
			return
		}
		*cfg.channels = append(*cfg.channels, entryID)
	case "guild":
		if utils.Contains(*cfg.guilds, entryID) {
			c.JSON(http.StatusOK, gin.H{"message": "already in " + listName})
			return
		}
		*cfg.guilds = append(*cfg.guilds, entryID)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid type: must be user, channel, or guild"})
		return
	}

	if err := config.Validate(s.config); err != nil {
		*s.config = previous
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid list update: " + err.Error()})
		return
	}

	if !s.saveConfigOrError(c) {
		*s.config = previous
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "added to " + listName,
	})
}

func (s *Server) removeFromList(c *gin.Context, listName string, cfg listConfig) {
	entryType := strings.ToLower(strings.TrimSpace(c.Param("type")))
	id := strings.TrimSpace(c.Param("id"))
	if entryType == "" || id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type and id are required"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	previous := *s.config

	switch entryType {
	case "user":
		*cfg.users = utils.RemoveFromSlice(*cfg.users, id)
	case "channel":
		*cfg.channels = utils.RemoveFromSlice(*cfg.channels, id)
	case "guild":
		*cfg.guilds = utils.RemoveFromSlice(*cfg.guilds, id)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid type: must be user, channel, or guild"})
		return
	}

	if err := config.Validate(s.config); err != nil {
		*s.config = previous
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid list update: " + err.Error()})
		return
	}

	if !s.saveConfigOrError(c) {
		*s.config = previous
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "removed from " + listName,
	})
}
