package shared

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// a secret-bearing line simulating a remote command's stderr/error that
// would otherwise be logged verbatim.
const secretLine = `mysql -uroot -pS3cretPass! -h10.0.0.5 'FLUSH TABLES WITH READ LOCK' exited 1: Access denied`

func newCapturingLogger() (*Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	l := NewLogger(buf, LevelDebug)
	return l, buf
}

func decodeLast(buf *bytes.Buffer) map[string]interface{} {
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return nil
	}
	var rec map[string]interface{}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		return nil
	}
	return rec
}

// TestStructuredLogRedactsMessage proves a secret in the message string is
// masked before it reaches the sink — the central redaction boundary.
func TestStructuredLogRedactsMessage(t *testing.T) {
	l, buf := newCapturingLogger()
	l.Error(secretLine)
	rec := decodeLast(buf)
	if rec == nil {
		t.Fatal("no record emitted")
	}
	if strings.Contains(rec["message"].(string), "S3cretPass!") {
		t.Fatalf("secret leaked into structured log message: %v", rec["message"])
	}
}

// TestStructuredLogRedactsStringValue proves a secret in a string-typed
// attribute is masked at the emit boundary, not just the message.
func TestStructuredLogRedactsStringValue(t *testing.T) {
	l, buf := newCapturingLogger()
	l.Error("command failed", "output", secretLine)
	rec := decodeLast(buf)
	if rec == nil {
		t.Fatal("no record emitted")
	}
	if strings.Contains(rec["output"].(string), "S3cretPass!") {
		t.Fatalf("secret leaked into structured log attribute: %v", rec["output"])
	}
	if !strings.Contains(rec["output"].(string), "[REDACTED]") {
		t.Fatalf("expected redaction marker in attribute: %v", rec["output"])
	}
}

// TestRedactWriterRedacts proves the standard-library reroute masks secrets
// in raw log.Printf output — the 80+ unstructured call sites.
func TestRedactWriterRedacts(t *testing.T) {
	buf := &bytes.Buffer{}
	w := redactWriter{w: buf}
	if _, err := w.Write([]byte(secretLine)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if strings.Contains(buf.String(), "S3cretPass!") {
		t.Fatalf("redactWriter leaked secret: %s", buf.String())
	}
}

// TestLoggerBaseFieldsPersist proves WithCorrelation adds identity to records.
func TestLoggerBaseFieldsPersist(t *testing.T) {
	l, buf := newCapturingLogger()
	ll := l.WithCorrelation("corr-abc", 42)
	ll.Info("handled request")
	rec := decodeLast(buf)
	if rec == nil {
		t.Fatal("no record emitted")
	}
	if rec["correlation_id"] != "corr-abc" {
		t.Fatalf("missing correlation_id: %v", rec)
	}
	if rec["migration_id"] != float64(42) {
		t.Fatalf("missing migration_id: %v", rec)
	}
}

// TestSecretScanAcrossSinks is the CI secret-scan guard: the same secret
// must never appear in any sink (structured + writer) after redaction.
func TestSecretScanAcrossSinks(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewLogger(buf, LevelDebug)
	l.Error("run failed", "stderr", secretLine)
	w := redactWriter{w: buf}
	_, _ = w.Write([]byte(secretLine))

	if strings.Contains(buf.String(), "S3cretPass!") {
		t.Fatalf("secret appeared in a sink: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "[REDACTED]") {
		t.Fatalf("expected redaction marker present: %s", buf.String())
	}
}
