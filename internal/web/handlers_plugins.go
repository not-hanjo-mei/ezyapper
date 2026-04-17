package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) listPlugins(c *gin.Context) {
	plugins := s.pluginManager.ListPluginsExt()
	c.JSON(http.StatusOK, gin.H{
		"plugins": plugins,
	})
}

func (s *Server) enablePlugin(c *gin.Context) {
	name := c.Param("name")
	if err := s.pluginManager.EnablePlugin(name); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if refresher, ok := s.discordFetcher.(PluginToolRefresher); ok && refresher != nil {
		refresher.RefreshPluginTools()
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "plugin enabled",
		"name":    name,
	})
}

func (s *Server) disablePlugin(c *gin.Context) {
	name := c.Param("name")
	if err := s.pluginManager.DisablePlugin(name); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if refresher, ok := s.discordFetcher.(PluginToolRefresher); ok && refresher != nil {
		refresher.RefreshPluginTools()
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "plugin disabled",
		"name":    name,
	})
}
