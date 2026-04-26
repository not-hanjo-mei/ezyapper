package web

import (
	"context"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/memory"

	"github.com/gin-gonic/gin"
)

func (s *Server) getMemories(c *gin.Context) {
	userID := c.Param("userID")

	limit := 50
	if raw, ok := c.GetQuery("limit"); ok {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a positive integer"})
			return
		}
		limit = parsed
	}

	memories, err := 	s.memoryStore.GetMemories(c.Request.Context(), userID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id":  userID,
		"count":    len(memories),
		"memories": memories,
	})
}

func (s *Server) searchMemories(c *gin.Context) {
	userID := c.Param("userID")
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' is required"})
		return
	}

	memories, err := 	s.memoryStore.Search(c.Request.Context(), userID, query, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id":  userID,
		"query":    query,
		"count":    len(memories),
		"memories": memories,
	})
}

func (s *Server) deleteMemory(c *gin.Context) {
	userID := c.Param("userID")
	memoryID := c.Param("id")

	memory, err := 	s.memoryStore.GetMemory(c.Request.Context(), memoryID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "memory not found"})
		return
	}
	if memory == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "memory not found"})
		return
	}

	if memory.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "memory does not belong to this user"})
		return
	}

	if err := 	s.memoryStore.DeleteMemory(c.Request.Context(), memoryID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "memory deleted",
	})
}

func (s *Server) clearMemories(c *gin.Context) {
	userID := c.Param("userID")

	if err := 	s.memoryStore.DeleteUserData(c.Request.Context(), userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "all user data deleted",
	})
}

func (s *Server) getProfile(c *gin.Context) {
	userID := c.Param("userID")

	profile, err := 	s.profileStore.GetProfile(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "profile not found"})
		return
	}

	c.JSON(http.StatusOK, profile)
}

func (s *Server) updateProfile(c *gin.Context) {
	userID := c.Param("userID")

	var profile memory.Profile
	if err := c.ShouldBindJSON(&profile); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	profile.UserID = userID

	if err := 	s.profileStore.UpdateProfile(c.Request.Context(), &profile); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "profile updated",
		"profile": profile,
	})
}

func (s *Server) deleteProfile(c *gin.Context) {
	userID := c.Param("userID")

	if err := 	s.memoryStore.DeleteUserData(c.Request.Context(), userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "profile deleted",
	})
}

func (s *Server) triggerConsolidation(c *gin.Context) {
	userID := c.Param("userID")
	channelID := c.Query("channel_id")

	maxMessages := 20
	if raw, ok := c.GetQuery("max_messages"); ok {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "max_messages must be a positive integer"})
			return
		}
		maxMessages = parsed
	}

	if maxMessages > 100 {
		maxMessages = 100
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[web] panic recovered: %v\n%s", r, debug.Stack())
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		if channelID != "" && s.discordFetcher != nil {
			messages, err := s.discordFetcher.FetchUserMessages(channelID, userID, maxMessages)
			if err != nil {
				logger.Errorf("Failed to fetch messages for consolidation: %v", err)
			if err := s.consolidationMgr.Consolidate(ctx, userID); err != nil {
				logger.Errorf("Consolidation fallback failed for user %s: %v", userID, err)
			}
			return
		}
		if len(messages) == 0 {
			logger.Warnf("No messages found for user %s in channel %s", userID, channelID)
			return
		}
		logger.Infof("Consolidating %d messages for user %s from channel %s", len(messages), userID, channelID)
		if err := s.consolidationMgr.ConsolidateWithMessages(ctx, userID, messages); err != nil {
			logger.Errorf("Consolidation with messages failed for user %s: %v", userID, err)
		}
	} else {
		logger.Warnf("No channel_id provided or discord fetcher not available, using fallback consolidation for user %s", userID)
		if err := s.consolidationMgr.Consolidate(ctx, userID); err != nil {
				logger.Errorf("Consolidation failed for user %s: %v", userID, err)
			}
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{
		"message":    "consolidation triggered",
		"user_id":    userID,
		"channel_id": channelID,
	})
}
