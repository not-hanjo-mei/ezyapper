// Package logger provides structured logging using Zap
package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Logger wraps zap.Logger for application-wide logging
type Logger struct {
	*zap.SugaredLogger
	atomicLevel zap.AtomicLevel
	// cfg is the configuration the logger was built from (zero for derived
	// or no-op loggers).
	cfg Config
	// closer closes the rotating log file, if any.
	closer io.Closer
}

// Config holds logger configuration
type Config struct {
	Level      string
	File       string
	MaxSize    int
	MaxBackups int
	MaxAge     int
}

// New creates a new Logger instance
func New(cfg Config) (*Logger, error) {
	// Parse log level
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", cfg.Level, err)
	}

	atomicLevel := zap.NewAtomicLevelAt(level)

	// Configure encoder
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// Create cores for different outputs
	cores := []zapcore.Core{}
	var closer io.Closer

	// Console output (always enabled)
	consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)
	consoleCore := zapcore.NewCore(
		consoleEncoder,
		zapcore.AddSync(os.Stdout),
		atomicLevel,
	)
	cores = append(cores, consoleCore)

	// File output (if configured)
	if cfg.File != "" {
		// Ensure log directory exists
		logDir := filepath.Dir(cfg.File)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return nil, err
		}

		fileWriter := &lumberjack.Logger{
			Filename:   cfg.File,
			MaxSize:    cfg.MaxSize,    // MB
			MaxBackups: cfg.MaxBackups, // Number of backups
			MaxAge:     cfg.MaxAge,     // Days
			Compress:   true,
		}
		closer = fileWriter

		fileEncoder := zapcore.NewJSONEncoder(encoderConfig)
		fileCore := zapcore.NewCore(
			fileEncoder,
			zapcore.AddSync(fileWriter),
			atomicLevel,
		)
		cores = append(cores, fileCore)
	}

	// Combine cores
	core := zapcore.NewTee(cores...)

	// Create logger with caller info
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))

	return &Logger{
		SugaredLogger: logger.Sugar(),
		atomicLevel:   atomicLevel,
		cfg:           cfg,
		closer:        closer,
	}, nil
}

// Config returns the configuration the logger was built from.
func (l *Logger) Config() Config {
	return l.cfg
}

// Close flushes the logger and closes its log file, if any.
func (l *Logger) Close() error {
	_ = l.SugaredLogger.Sync()
	if l.closer != nil {
		return l.closer.Close()
	}
	return nil
}

// SetLevel dynamically changes the log level
func (l *Logger) SetLevel(level string) error {
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		return err
	}
	l.atomicLevel.SetLevel(zapLevel)
	return nil
}

// Level returns the current log level
func (l *Logger) Level() string {
	return l.atomicLevel.Level().String()
}

// With creates a child logger with additional context
func (l *Logger) With(fields ...any) *Logger {
	return &Logger{
		SugaredLogger: l.SugaredLogger.With(fields...),
		atomicLevel:   l.atomicLevel,
		cfg:           l.cfg,
	}
}

// Named adds a sub-logger name
func (l *Logger) Named(name string) *Logger {
	return &Logger{
		SugaredLogger: l.SugaredLogger.Named(name),
		atomicLevel:   l.atomicLevel,
		cfg:           l.cfg,
	}
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() error {
	return l.SugaredLogger.Sync()
}

// Global logger instance. It is an atomic pointer so config hot reload can
// replace it (new log file, new rotation settings) while other goroutines log.
var globalLogger atomic.Pointer[Logger]

var noopLogger = &Logger{SugaredLogger: zap.NewNop().Sugar()}
var noopWarnOnce sync.Once

// Init initializes the global logger
func Init(cfg Config) error {
	l, err := New(cfg)
	if err != nil {
		return err
	}
	globalLogger.Store(l)
	return nil
}

// oldLoggerGrace is how long a replaced logger's file stays open, so log
// calls that already loaded the old pointer can finish writing.
const oldLoggerGrace = 5 * time.Second

// Replace installs l as the global logger and closes the previous one's log
// file after a short grace period. It returns the previous logger (nil if
// none was initialized).
func Replace(l *Logger) *Logger {
	if l == nil {
		return nil
	}
	old := globalLogger.Swap(l)
	if old != nil && old.closer != nil {
		time.AfterFunc(oldLoggerGrace, func() { _ = old.Close() })
	}
	return old
}

// NeedsRebuild reports whether moving from a to b needs a new logger (new
// file or rotation settings). A level-only change does not: it is applied
// to the existing logger with SetLevel.
func NeedsRebuild(a, b Config) bool {
	return a.File != b.File || a.MaxSize != b.MaxSize || a.MaxBackups != b.MaxBackups || a.MaxAge != b.MaxAge
}

// Reconfigure applies cfg to the global logger: a level-only change is set
// in place; a file or rotation change builds a new logger and swaps it in.
// On error the current logger is left untouched.
func Reconfigure(cfg Config) error {
	cur := globalLogger.Load()
	if cur == nil {
		return Init(cfg)
	}
	if !NeedsRebuild(cur.cfg, cfg) {
		if err := cur.SetLevel(cfg.Level); err != nil {
			return fmt.Errorf("invalid log level %q: %w", cfg.Level, err)
		}
		cur.cfg.Level = cfg.Level
		return nil
	}
	l, err := New(cfg)
	if err != nil {
		return err
	}
	Replace(l)
	return nil
}

// L returns the global logger. If the logger has not been initialized,
// returns a no-op logger that silently discards all output rather than
// panicking — library code should never crash the process.
func L() *Logger {
	l := globalLogger.Load()
	if l == nil {
		noopWarnOnce.Do(func() {
			fmt.Fprintln(os.Stderr, "WARNING: logger.L() called before Init() — using no-op logger")
		})
		return noopLogger
	}
	return l
}

// Convenience functions using the global logger

// Debug logs a debug message
func Debug(args ...any) {
	L().Debug(args...)
}

// Debugf logs a formatted debug message
func Debugf(template string, args ...any) {
	L().Debugf(template, args...)
}

// Info logs an info message
func Info(args ...any) {
	L().Info(args...)
}

// Infof logs a formatted info message
func Infof(template string, args ...any) {
	L().Infof(template, args...)
}

// Warn logs a warning message
func Warn(args ...any) {
	L().Warn(args...)
}

// Warnf logs a formatted warning message
func Warnf(template string, args ...any) {
	L().Warnf(template, args...)
}

// Error logs an error message
func Error(args ...any) {
	L().Error(args...)
}

// Errorf logs a formatted error message
func Errorf(template string, args ...any) {
	L().Errorf(template, args...)
}

// Fatalf logs a formatted fatal message and exits
func Fatalf(template string, args ...any) {
	L().Fatalf(template, args...)
}

// With creates a child logger with additional context
func With(fields ...any) *Logger {
	return L().With(fields...)
}

// Named adds a sub-logger name
func Named(name string) *Logger {
	return L().Named(name)
}

// ValidateLevel reports whether level is a valid log level name.
func ValidateLevel(level string) error {
	var l zapcore.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return fmt.Errorf("invalid log level %q: %w", level, err)
	}
	return nil
}
