package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/hotreload"
	"ezyapper/internal/logger"
	"ezyapper/internal/plugin"
)

// LoggerConfig converts operations.logging to the logger's config.
func LoggerConfig(c config.LoggingConfig) logger.Config {
	return logger.Config{
		Level:      c.Level,
		File:       c.File,
		MaxSize:    c.MaxSize,
		MaxBackups: c.MaxBackups,
		MaxAge:     c.MaxAge,
	}
}

// LoggingComponent applies operations.logging.*: a level change is set on
// the running logger; a file or rotation change opens the new log file in
// Prepare (so an unwritable path rejects the reload) and swaps loggers in
// Commit.
type LoggingComponent struct{}

func (LoggingComponent) Name() string { return "logging" }

func (LoggingComponent) Prepare(_ context.Context, old, next *config.Config, changes hotreload.Changes) (hotreload.Pending, error) {
	if !changes.Any("operations.logging") {
		return nil, nil
	}
	oc, nc := LoggerConfig(old.Logging), LoggerConfig(next.Logging)
	if !logger.NeedsRebuild(oc, nc) {
		if err := logger.ValidateLevel(nc.Level); err != nil {
			return nil, err
		}
		return hotreload.Funcs{CommitFunc: func() {
			if err := logger.L().SetLevel(nc.Level); err != nil {
				logger.Errorf("[hotreload] set log level: %v", err)
				return
			}
			logger.Infof("[hotreload] log level set to %s", nc.Level)
		}}, nil
	}
	l, err := logger.New(nc)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", nc.File, err)
	}
	return hotreload.Funcs{
		CommitFunc: func() {
			logger.Replace(l)
			logger.Infof("[hotreload] logging reconfigured (file %s, level %s)", nc.File, nc.Level)
		},
		AbortFunc: func() { _ = l.Close() },
	}, nil
}

// PluginSettings converts operations.plugins to the manager's settings.
func PluginSettings(c config.PluginsConfig) plugin.Settings {
	return plugin.Settings{
		DefaultToolTimeoutMs: c.DefaultToolTimeoutMs,
		StartupTimeoutSec:    c.StartupTimeoutSec,
		RPCTimeoutSec:        c.RPCTimeoutSec,
		BeforeSendTimeoutSec: c.BeforeSendTimeoutSec,
		CommandTimeoutSec:    c.CommandTimeoutSec,
		ShutdownTimeoutSec:   c.ShutdownTimeoutSec,
		DisableTimeoutSec:    c.DisableTimeoutSec,
		MaxRestarts:          c.MaxRestarts,
		RestartCooldown:      time.Duration(c.RestartCooldownSec) * time.Second,
	}
}

// PluginsComponent applies operations.plugins.* on the host side. Timeouts
// and the restart policy are swapped into the running manager. Turning
// plugins on/off or changing plugins_dir stops every plugin and loads the
// new directory. A plugin's own config file is not handled here: each
// plugin watches it itself (internal/plugin/pluginconfig) and reloads
// without the host being involved.
type PluginsComponent struct {
	Manager *plugin.Manager
}

func (PluginsComponent) Name() string { return "plugins" }

func (p PluginsComponent) Prepare(_ context.Context, old, next *config.Config, changes hotreload.Changes) (hotreload.Pending, error) {
	if !changes.Any("operations.plugins") || p.Manager == nil {
		return nil, nil
	}
	o, n := old.Plugins, next.Plugins
	reload := o.Enabled != n.Enabled || o.PluginsDir != n.PluginsDir
	if reload && n.Enabled {
		st, err := os.Stat(n.PluginsDir)
		if err != nil {
			return nil, fmt.Errorf("operations.plugins.plugins_dir: %w", err)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("operations.plugins.plugins_dir: %s is not a directory", n.PluginsDir)
		}
	}
	return hotreload.Funcs{CommitFunc: func() {
		p.Manager.UpdateSettings(PluginSettings(n))
		if !reload {
			logger.Info("[hotreload] plugin timeouts and restart policy updated")
			return
		}
		// Plugins are processes: they cannot be started next to the old
		// set without running twice, so this step runs at commit time.
		// A plugin that fails to start is logged; the others keep running.
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(n.ShutdownTimeoutSec+n.StartupTimeoutSec+5)*time.Second)
		defer cancel()
		if err := p.Manager.ReloadFromDir(ctx, n.PluginsDir, n.Enabled); err != nil {
			logger.Errorf("[hotreload] plugin reload from %s: %v", n.PluginsDir, err)
			return
		}
		if n.Enabled {
			logger.Infof("[hotreload] plugins reloaded from %s", n.PluginsDir)
		} else {
			logger.Info("[hotreload] plugins disabled and stopped")
		}
	}}, nil
}
