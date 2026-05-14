package logging

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogLevel represents the verbosity level
type LogLevel string

const (
	LevelDebug   LogLevel = "debug"
	LevelInfo    LogLevel = "info"
	LevelWarn    LogLevel = "warn"
	LevelError   LogLevel = "error"
)

var (
	currentLevel  LogLevel = LevelInfo
	logFile       *os.File
	stderrLogger  *log.Logger
	fileLogger    *log.Logger
	disableStderr bool
	mu            sync.Mutex
	once          sync.Once
)

// Config holds logging configuration
type Config struct {
	Level        LogLevel
	LogFile      string // Path to log file (empty = no file logging)
	Pretty       bool   // Pretty print output (controls stderr format)
	DisableStderr bool   // Disable stderr output (useful for TUI apps)
}

// Init initializes the logger with configuration
func Init(cfg Config) error {
	var err error
	once.Do(func() {
		currentLevel = cfg.Level
		disableStderr = cfg.DisableStderr

		if !cfg.DisableStderr {
			stderrLogger = log.New(os.Stderr, "", 0)
		}

		// Add file output if configured
		if cfg.LogFile != "" {
			// Ensure log directory exists
			logDir := filepath.Dir(cfg.LogFile)
			if logDir != "." && logDir != "" {
				if errDir := os.MkdirAll(logDir, 0700); errDir != nil {
					err = fmt.Errorf("failed to create log directory: %w", errDir)
					return
				}
			}

			// Open log file
			f, errFile := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if errFile != nil {
				err = fmt.Errorf("failed to open log file: %w", errFile)
				return
			}
			logFile = f
			fileLogger = log.New(logFile, "", 0)
		}
	})

	return err
}

// getLevelInt returns numeric level for comparison (higher number = more important)
func getLevelInt(level LogLevel) int {
	switch level {
	case LevelDebug:
		return 0
	case LevelInfo:
		return 1
	case LevelWarn:
		return 2
	case LevelError:
		return 3
	default:
		return 1
	}
}

// formatJSON formats a log entry as JSON
func formatJSON(level, message string) string {
	entry := map[string]interface{}{
		"level":   level,
		"msg":     message,
		"time":    time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
	}
	b, _ := json.Marshal(entry)
	return string(b) + "\n"
}

// formatPretty formats a log entry for human reading
func formatPretty(level, message string) string {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	levelStr := fmt.Sprintf("%-5s", level)
	return fmt.Sprintf("[%s] %s %s\n", timestamp, levelStr, message)
}

// log writes to both stderr and file with the given level
func logMessage(level LogLevel, message string) {
	// Check if we should log at this level
	if getLevelInt(level) < getLevelInt(currentLevel) {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	// Log to stderr (pretty format) unless disabled
	if !disableStderr && stderrLogger != nil {
		stderrLogger.Print(formatPretty(string(level), message))
	}

	// Log to file (JSON format) if configured
	if fileLogger != nil {
		fileLogger.Print(formatJSON(string(level), message))
	}
}

// IsDebug reports whether the current log level is debug, allowing callers to
// skip expensive message formatting that would otherwise be discarded.
func IsDebug() bool {
	return getLevelInt(currentLevel) <= getLevelInt(LevelDebug)
}

// Convenience functions
func Debug(args ...interface{}) {
	logMessage(LevelDebug, fmt.Sprint(args...))
}

func Debugf(format string, args ...interface{}) {
	logMessage(LevelDebug, fmt.Sprintf(format, args...))
}

func Info(args ...interface{}) {
	logMessage(LevelInfo, fmt.Sprint(args...))
}

func Infof(format string, args ...interface{}) {
	logMessage(LevelInfo, fmt.Sprintf(format, args...))
}

func Warn(args ...interface{}) {
	logMessage(LevelWarn, fmt.Sprint(args...))
}

func Warnf(format string, args ...interface{}) {
	logMessage(LevelWarn, fmt.Sprintf(format, args...))
}

func Error(args ...interface{}) {
	logMessage(LevelError, fmt.Sprint(args...))
}

func Errorf(format string, args ...interface{}) {
	logMessage(LevelError, fmt.Sprintf(format, args...))
}

// WithError wraps an error for logging (stub for API compatibility)
func WithError(err error) interface{} {
	return err
}

// WithField wraps a field for logging (stub for API compatibility)
func WithField(key string, value interface{}) interface{} {
	return fmt.Sprintf("%s=%v", key, value)
}
