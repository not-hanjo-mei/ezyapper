package web

import (
	"net/http"
	"strings"

	"ezyapper/internal/config"
	"ezyapper/internal/utils"

	"github.com/gin-gonic/gin"
)

type listEntryRequest struct {
	Type string `json:"type" binding:"required"`
	ID   string `json:"id" binding:"required"`
}

func copySlice(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	c := make([]string, len(s))
	copy(c, s)
	return c
}

func (s *Server) getBlacklist(c *gin.Context) {
	cfg := s.cfg()
	c.JSON(http.StatusOK, gin.H{
		"users":    cfg.Blacklist.Users,
		"channels": cfg.Blacklist.Channels,
		"guilds":   cfg.Blacklist.Guilds,
	})
}

func (s *Server) addToBlacklist(c *gin.Context) {
	var req listEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	entryType := strings.ToLower(strings.TrimSpace(req.Type))
	entryID := strings.TrimSpace(req.ID)

	oldCfg := s.cfg()
	newCfg := *oldCfg

	switch entryType {
	case "user":
		if utils.Contains(oldCfg.Blacklist.Users, entryID) {
			c.JSON(http.StatusOK, gin.H{"message": "already in blacklist"})
			return
		}
		newCfg.Blacklist.Users = append(copySlice(oldCfg.Blacklist.Users), entryID)
	case "channel":
		if utils.Contains(oldCfg.Blacklist.Channels, entryID) {
			c.JSON(http.StatusOK, gin.H{"message": "already in blacklist"})
			return
		}
		newCfg.Blacklist.Channels = append(copySlice(oldCfg.Blacklist.Channels), entryID)
	case "guild":
		if utils.Contains(oldCfg.Blacklist.Guilds, entryID) {
			c.JSON(http.StatusOK, gin.H{"message": "already in blacklist"})
			return
		}
		newCfg.Blacklist.Guilds = append(copySlice(oldCfg.Blacklist.Guilds), entryID)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid type: must be user, channel, or guild"})
		return
	}

	if err := config.Validate(&newCfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid list update: " + err.Error()})
		return
	}

	s.configStore.Store(&newCfg)

	if !s.saveConfigOrError(c, &newCfg) {
		s.configStore.Store(oldCfg)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "added to blacklist",
	})
}

func (s *Server) removeFromBlacklist(c *gin.Context) {
	entryType := strings.ToLower(strings.TrimSpace(c.Param("type")))
	id := strings.TrimSpace(c.Param("id"))
	if entryType == "" || id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type and id are required"})
		return
	}

	oldCfg := s.cfg()
	newCfg := *oldCfg

	switch entryType {
	case "user":
		newCfg.Blacklist.Users = utils.RemoveFromSlice(oldCfg.Blacklist.Users, id)
	case "channel":
		newCfg.Blacklist.Channels = utils.RemoveFromSlice(oldCfg.Blacklist.Channels, id)
	case "guild":
		newCfg.Blacklist.Guilds = utils.RemoveFromSlice(oldCfg.Blacklist.Guilds, id)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid type: must be user, channel, or guild"})
		return
	}

	if err := config.Validate(&newCfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid list update: " + err.Error()})
		return
	}

	s.configStore.Store(&newCfg)

	if !s.saveConfigOrError(c, &newCfg) {
		s.configStore.Store(oldCfg)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "removed from blacklist",
	})
}

func (s *Server) getWhitelist(c *gin.Context) {
	cfg := s.cfg()
	c.JSON(http.StatusOK, gin.H{
		"users":    cfg.Whitelist.Users,
		"channels": cfg.Whitelist.Channels,
		"guilds":   cfg.Whitelist.Guilds,
	})
}

func (s *Server) addToWhitelist(c *gin.Context) {
	var req listEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	entryType := strings.ToLower(strings.TrimSpace(req.Type))
	entryID := strings.TrimSpace(req.ID)

	oldCfg := s.cfg()
	newCfg := *oldCfg

	switch entryType {
	case "user":
		if utils.Contains(oldCfg.Whitelist.Users, entryID) {
			c.JSON(http.StatusOK, gin.H{"message": "already in whitelist"})
			return
		}
		newCfg.Whitelist.Users = append(copySlice(oldCfg.Whitelist.Users), entryID)
	case "channel":
		if utils.Contains(oldCfg.Whitelist.Channels, entryID) {
			c.JSON(http.StatusOK, gin.H{"message": "already in whitelist"})
			return
		}
		newCfg.Whitelist.Channels = append(copySlice(oldCfg.Whitelist.Channels), entryID)
	case "guild":
		if utils.Contains(oldCfg.Whitelist.Guilds, entryID) {
			c.JSON(http.StatusOK, gin.H{"message": "already in whitelist"})
			return
		}
		newCfg.Whitelist.Guilds = append(copySlice(oldCfg.Whitelist.Guilds), entryID)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid type: must be user, channel, or guild"})
		return
	}

	if err := config.Validate(&newCfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid list update: " + err.Error()})
		return
	}

	s.configStore.Store(&newCfg)

	if !s.saveConfigOrError(c, &newCfg) {
		s.configStore.Store(oldCfg)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "added to whitelist",
	})
}

func (s *Server) removeFromWhitelist(c *gin.Context) {
	entryType := strings.ToLower(strings.TrimSpace(c.Param("type")))
	id := strings.TrimSpace(c.Param("id"))
	if entryType == "" || id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type and id are required"})
		return
	}

	oldCfg := s.cfg()
	newCfg := *oldCfg

	switch entryType {
	case "user":
		newCfg.Whitelist.Users = utils.RemoveFromSlice(oldCfg.Whitelist.Users, id)
	case "channel":
		newCfg.Whitelist.Channels = utils.RemoveFromSlice(oldCfg.Whitelist.Channels, id)
	case "guild":
		newCfg.Whitelist.Guilds = utils.RemoveFromSlice(oldCfg.Whitelist.Guilds, id)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid type: must be user, channel, or guild"})
		return
	}

	if err := config.Validate(&newCfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid list update: " + err.Error()})
		return
	}

	s.configStore.Store(&newCfg)

	if !s.saveConfigOrError(c, &newCfg) {
		s.configStore.Store(oldCfg)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "removed from whitelist",
	})
}
