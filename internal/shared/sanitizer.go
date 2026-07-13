package shared

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
)

// Use SanitizeString() when logging user-provided data, error messages that may contain
// credentials, or any output that could leak secrets.
var (
	// Passwords in connection strings: password=xxx, pwd=xxx, pass=xxx
	passwordPattern = regexp.MustCompile(`(?i)(password|passwd|pwd|pass|secret|token|api_key|apikey|access_key|private_key|credential)\s*[=:]\s*\S+`)
	// MySQL/psql inline password flag: `mysql -pSECRET` or `mysql -p SECRET`.
	// The lowercase `-p` form is the DB client password convention (ports use
	// `-P`); redacting it is the only way to stop the most common raw-secret
	// leak in command output / error strings. A false-positive redaction of a
	// numeric port is harmless in an operator log.
	inlinePasswordPattern = regexp.MustCompile(`(?i)-p\s*([^\s-][^\s]*)`)
	// Bearer tokens
	bearerPattern = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9\-._~+/]+=*`)
	// SSH private key blocks
	privateKeyPattern = regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`)
	// Base64-encoded long strings (likely tokens/keys) — only mask if >40 chars
	longBase64Pattern = regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`)
	// Authorization headers
	authHeaderPattern = regexp.MustCompile(`(?i)authorization\s*:\s*.*`)
)

// SanitizeString removes secrets from a string, replacing them with [REDACTED].
func SanitizeString(s string) string {
	// MySQL/psql inline password flag BEFORE other patterns: `mysql -uroot
	// -pSECRET ...` is the most common raw-secret leak in command lines,
	// error strings, and remote stderr. Run first so the replacement text
	// cannot be re-matched by later patterns. A space after `-p` is left
	// intact (that is an SSH/port flag, not a password).
	s = inlinePasswordPattern.ReplaceAllString(s, "-p [REDACTED]")
	s = privateKeyPattern.ReplaceAllString(s, "[REDACTED-PRIVATE-KEY]")
	s = authHeaderPattern.ReplaceAllString(s, "Authorization: [REDACTED]")
	s = bearerPattern.ReplaceAllString(s, "Bearer [REDACTED]")
	s = passwordPattern.ReplaceAllStringFunc(s, func(m string) string {
		// Keep the key name, redact the value.
		parts := regexp.MustCompile(`(?i)(password|passwd|pwd|pass|secret|token|api_key|apikey|access_key|private_key|credential)\s*[=:]`).FindString(m)
		if parts != "" {
			return parts + " [REDACTED]"
		}
		return "[REDACTED]"
	})
	s = longBase64Pattern.ReplaceAllString(s, "[REDACTED]")
	return s
}

// SanitizeLog sanitizes a log message and returns the cleaned version.
func SanitizeLog(format string, args ...interface{}) string {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	return SanitizeString(msg)
}

// LogPrintf is a drop-in replacement for log.Printf that sanitizes secrets.
func LogPrintf(format string, args ...interface{}) {
	log.Printf("%s", SanitizeLog(format, args...))
}

// LogPrintln is a drop-in replacement for log.Println that sanitizes secrets.
func LogPrintln(args ...interface{}) {
	log.Println(SanitizeString(fmt.Sprint(args...)))
}

// SanitizeJSONRawMessage sanitizes string values contained in a JSON payload.
func SanitizeJSONRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}

	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return json.RawMessage(SanitizeString(string(raw)))
	}

	sanitized := sanitizeJSONValue(payload)
	cleaned, err := json.Marshal(sanitized)
	if err != nil {
		return json.RawMessage(SanitizeString(string(raw)))
	}

	return json.RawMessage(cleaned)
}

func sanitizeJSONValue(v any) any {
	switch value := v.(type) {
	case string:
		return SanitizeString(value)
	case map[string]any:
		for k, item := range value {
			value[k] = sanitizeJSONValue(item)
		}
		return value
	case []any:
		for i, item := range value {
			value[i] = sanitizeJSONValue(item)
		}
		return value
	default:
		return v
	}
}
