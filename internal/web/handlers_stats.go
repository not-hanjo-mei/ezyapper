package web

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) getLogs(c *gin.Context) {
	lines := 100
	if raw, ok := c.GetQuery("lines"); ok {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "lines must be a positive integer"})
			return
		}
		lines = parsed
	}

	if lines > 5000 {
		lines = 5000
	}

	logFile := s.config.Logging.File
	if logFile == "" {
		c.JSON(http.StatusOK, gin.H{"logs": []string{}})
		return
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"logs": []string{}, "error": "could not read log file"})
		return
	}

	allLines := strings.Split(string(data), "\n")
	start := max(len(allLines)-lines, 0)

	c.JSON(http.StatusOK, gin.H{
		"logs": allLines[start:],
	})
}

func (s *Server) getStats(c *gin.Context) {
	stats, err := s.memory.GetStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"uptime": time.Since(s.startTime).Seconds(),
		"stats":  stats,
	})
}

func (s *Server) getUserStats(c *gin.Context) {
	userID := c.Param("userID")

	stats, err := s.memory.GetUserStats(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, stats)
}
