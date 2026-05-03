// Package logger provides structured logging using Zap
package logger

import (
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Logger wraps zap.Logger for application-wide logging
type Logger struct {
	*zap.SugaredLogger
	atomicLevel zap.AtomicLevel
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
	}, nil
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

// GetLevel returns the current log level
func (l *Logger) GetLevel() string {
	return l.atomicLevel.Level().String()
}

// With creates a child logger with additional context
func (l *Logger) With(fields ...interface{}) *Logger {
	return &Logger{
		SugaredLogger: l.SugaredLogger.With(fields...),
		atomicLevel:   l.atomicLevel,
	}
}

// Named adds a sub-logger name
func (l *Logger) Named(name string) *Logger {
	return &Logger{
		SugaredLogger: l.SugaredLogger.Named(name),
		atomicLevel:   l.atomicLevel,
	}
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() error {
	return l.SugaredLogger.Sync()
}

// Global logger instance
var globalLogger *Logger

// Init initializes the global logger
func Init(cfg Config) error {
	var err error
	globalLogger, err = New(cfg)
	return err
}

// L returns the global logger. If the logger has not been initialized,
// the function panics rather than returning nil — there is no silent fallback.
func L() *Logger {
	if globalLogger == nil {
		panic("logger.L() called before Init() — logger was not initialized")
	}
	return globalLogger
}

// Convenience functions using the global logger

// Debug logs a debug message
func Debug(args ...interface{}) {
	L().Debug(args...)
}

// Debugf logs a formatted debug message
func Debugf(template string, args ...interface{}) {
	L().Debugf(template, args...)
}

// Info logs an info message
func Info(args ...interface{}) {
	L().Info(args...)
}

// Infof logs a formatted info message
func Infof(template string, args ...interface{}) {
	L().Infof(template, args...)
}

// Warn logs a warning message
func Warn(args ...interface{}) {
	L().Warn(args...)
}

// Warnf logs a formatted warning message
func Warnf(template string, args ...interface{}) {
	L().Warnf(template, args...)
}

// Error logs an error message
func Error(args ...interface{}) {
	L().Error(args...)
}

// Errorf logs a formatted error message
func Errorf(template string, args ...interface{}) {
	L().Errorf(template, args...)
}

// Fatal logs a fatal message and exits
func Fatal(args ...interface{}) {
	L().Fatal(args...)
}

// Fatalf logs a formatted fatal message and exits
func Fatalf(template string, args ...interface{}) {
	L().Fatalf(template, args...)
}

// With creates a child logger with additional context
func With(fields ...interface{}) *Logger {
	return L().With(fields...)
}

// Named adds a sub-logger name
func Named(name string) *Logger {
	return L().Named(name)
}
