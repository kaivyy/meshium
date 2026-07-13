package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// correlationExtractor pulls active identity out of a context so structured
// logs can carry it without import coupling. Populated by the migration
// package via SetCorrelationExtractor; nil-safe.
var correlationExtractor func(context.Context) (corrID string, migrationID int)

// SetCorrelationExtractor registers the hook that threads correlation id /
// migration id into shared.Log output. Called once at startup.
func SetCorrelationExtractor(fn func(context.Context) (string, int)) {
	correlationExtractor = fn
}

// redactWriter sanitizes anything written through the standard library
// `log` package before it reaches the sink. Every log.Printf site (the 80+
// unstructured calls across the migration package, many of which echo command
// lines / remote stderr that can carry credentials) is therefore redacted at
// the one emit boundary, with no per-site edits.
type redactWriter struct {
	w io.Writer
}

func (r redactWriter) Write(p []byte) (int, error) {
	clean := SanitizeString(string(p))
	if _, err := r.w.Write([]byte(clean)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// InitLogging routes the standard library logger through the redaction boundary
// and threads correlation identity into the structured logger. Call once from
// main, after config load. Safe to call multiple times.
func InitLogging() {
	log.SetOutput(redactWriter{w: os.Stderr})
	log.SetFlags(0)
	// shared.Log already sanitizes at emit; InitLogging only wires the
	// stdlib reroute + lets callers adopt shared.Log with identity fields.
}

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
	mu         sync.Mutex
	out        io.Writer
	minLevel   LogLevel
	baseFields []interface{}
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

// LogCtx returns a logger that auto-threads the correlation id / migration id
// extracted from ctx (via SetCorrelationExtractor) into every record. Use it
// for new structured log sites in request/correlation-aware code paths so a
// log line can be tied back to its operation. Redaction still applies.
func LogCtx(ctx context.Context) *Logger {
	if correlationExtractor == nil {
		return Log
	}
	corr, mid := correlationExtractor(ctx)
	if corr == "" && mid == 0 {
		return Log
	}
	return Log.WithCorrelation(corr, mid)
}

// WithCorrelation returns a copy of l whose records carry the given identity.
func (l *Logger) WithCorrelation(corrID string, migrationID int) *Logger {
	extra := []interface{}{"correlation_id", corrID, "migration_id", migrationID}
	bf := make([]interface{}, 0, len(l.baseFields)+len(extra))
	bf = append(bf, l.baseFields...)
	bf = append(bf, extra...)
	return &Logger{
		out:        l.out,
		minLevel:   l.minLevel,
		baseFields: bf,
	}
}

func (l *Logger) log(level LogLevel, message string, keyValues ...interface{}) {
	if l == nil || level < l.minLevel {
		return
	}

	// Phase2D-3: the logger is a central redaction boundary. Sanitize the
	// message and every string-typed value so command lines, stderr, and
	// error strings that carry credentials are masked before they reach
	// any sink (stdout, file, aggregator). SanitizeString only masks
	// secret-shaped substrings, so non-secret values pass through intact.
	record := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"level":     level.String(),
		"message":   SanitizeString(message),
	}

	for i := 0; i+1 < len(l.baseFields); i += 2 {
		key := fmt.Sprint(l.baseFields[i])
		if key == "" {
			continue
		}
		record[key] = l.baseFields[i+1]
	}

	for i := 0; i+1 < len(keyValues); i += 2 {
		key := fmt.Sprint(keyValues[i])
		if key == "" {
			continue
		}
		if err, ok := keyValues[i+1].(error); ok {
			record[key] = SanitizeString(err.Error())
			continue
		}
		if s, ok := keyValues[i+1].(string); ok {
			record[key] = SanitizeString(s)
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
