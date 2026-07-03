package shared

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

// Log is the process-wide structured logger used by the application.
var Log = NewLogger(os.Stdout, LevelInfo)

// Logger writes JSON structured logs with a minimum level threshold.
type Logger struct {
	mu       sync.Mutex
	out      io.Writer
	minLevel LogLevel
}

// NewLogger creates a structured logger that writes JSON to out.
func NewLogger(out io.Writer, minLevel LogLevel) *Logger {
	if out == nil {
		out = io.Discard
	}
	return &Logger{out: out, minLevel: minLevel}
}

// SetLogger replaces the process-wide structured logger.
func SetLogger(l *Logger) {
	if l == nil {
		return
	}
	Log = l
}

// SetMinLevel updates the minimum level the logger will emit.
func (l *Logger) SetMinLevel(minLevel LogLevel) {
	if l == nil {
		return
	}
	l.minLevel = minLevel
}

// ParseLogLevel converts a string into a LogLevel.
func ParseLogLevel(level string) LogLevel {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return fmt.Sprintf("level(%d)", int(l))
	}
}

// Debug emits a debug log entry.
func (l *Logger) Debug(message string, keyValues ...interface{}) {
	l.log(LevelDebug, message, keyValues...)
}

// Info emits an info log entry.
func (l *Logger) Info(message string, keyValues ...interface{}) {
	l.log(LevelInfo, message, keyValues...)
}

// Warn emits a warning log entry.
func (l *Logger) Warn(message string, keyValues ...interface{}) {
	l.log(LevelWarn, message, keyValues...)
}

// Error emits an error log entry.
func (l *Logger) Error(message string, keyValues ...interface{}) {
	l.log(LevelError, message, keyValues...)
}

func (l *Logger) log(level LogLevel, message string, keyValues ...interface{}) {
	if l == nil || level < l.minLevel {
		return
	}

	record := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"level":     level.String(),
		"message":   message,
	}

	for i := 0; i+1 < len(keyValues); i += 2 {
		key := fmt.Sprint(keyValues[i])
		if key == "" {
			continue
		}
		if err, ok := keyValues[i+1].(error); ok {
			record[key] = err.Error()
			continue
		}
		record[key] = keyValues[i+1]
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	encoder := json.NewEncoder(l.out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(record)
}
