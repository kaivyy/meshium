package shared

import (
	"strings"
	"testing"
)

func TestSanitizeStringMasksPasswordInConnectionString(t *testing.T) {
	input := "host=localhost user=admin password=supersecret dbname=test"
	got := SanitizeString(input)

	if strings.Contains(got, "supersecret") {
		t.Fatalf("expected password to be redacted, got %q", got)
	}
	if !strings.Contains(got, "password= [REDACTED]") {
		t.Fatalf("expected password redaction, got %q", got)
	}
}

func TestSanitizeStringMasksBearerToken(t *testing.T) {
	input := "Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123456789"
	got := SanitizeString(input)

	if strings.Contains(got, "abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Fatalf("expected bearer token to be redacted, got %q", got)
	}
	if got != "Authorization: [REDACTED]" {
		t.Fatalf("unexpected bearer redaction: %q", got)
	}
}

func TestSanitizeStringMasksSSHPrivateKey(t *testing.T) {
	input := "-----BEGIN OPENSSH PRIVATE KEY-----\nvery-secret\n-----END OPENSSH PRIVATE KEY-----"
	got := SanitizeString(input)

	if got != "[REDACTED-PRIVATE-KEY]" {
		t.Fatalf("unexpected private key redaction: %q", got)
	}
}

func TestSanitizeStringMasksAPIKey(t *testing.T) {
	input := "api_key=myapikey123"
	got := SanitizeString(input)

	if strings.Contains(got, "myapikey123") {
		t.Fatalf("expected api key to be redacted, got %q", got)
	}
	if !strings.Contains(got, "api_key= [REDACTED]") {
		t.Fatalf("expected api key redaction, got %q", got)
	}
}

func TestSanitizeStringDoesNotChangeNormalMessages(t *testing.T) {
	input := "server started successfully"
	got := SanitizeString(input)

	if got != input {
		t.Fatalf("expected normal message to remain unchanged, got %q", got)
	}
}

func TestSanitizeStringDoesNotChangeServerNotFoundErrors(t *testing.T) {
	input := "server not found"
	got := SanitizeString(input)

	if got != input {
		t.Fatalf("expected server not found message to remain unchanged, got %q", got)
	}
}

func TestSanitizeStringMasksLongBase64String(t *testing.T) {
	input := "token=ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	got := SanitizeString(input)

	if strings.Contains(got, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/") {
		t.Fatalf("expected long base64 string to be redacted, got %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected long base64 redaction, got %q", got)
	}
}

func TestSanitizeStringLeavesShortStringsUntouched(t *testing.T) {
	input := "short123"
	got := SanitizeString(input)

	if got != input {
		t.Fatalf("expected short string to remain unchanged, got %q", got)
	}
}
