package migration

import (
	"strings"
	"testing"
)

// Progress messages carry raw remote stderr and command text straight to the
// browser and into the persisted event log. Sanitisation was applied at a
// handful of call sites (packages apply, event bus, cutover errors) and
// nowhere else, so any stage that surfaced a failing command leaked whatever
// that command's arguments contained.
//
// SanitizeWSMessage is the single choke point every outbound message passes
// through, so a new stage cannot forget it.
func TestSanitizeWSMessageRedactsValueAndError(t *testing.T) {
	msg := WSMessage{
		Step:   "initial_sync:database",
		Status: "error",
		Value:  "running: mysql -uroot -pSuperSecret123 -e 'SELECT 1'",
		Error:  "PGPASSWORD=hunter2 psql failed: authentication error",
	}

	got := SanitizeWSMessage(msg)

	if strings.Contains(got.Value, "SuperSecret123") {
		t.Errorf("inline mysql password survived into Value: %q", got.Value)
	}
	if strings.Contains(got.Error, "hunter2") {
		t.Errorf("PGPASSWORD value survived into Error: %q", got.Error)
	}
	// The message must stay useful — redaction is not deletion.
	if !strings.Contains(got.Value, "mysql") {
		t.Errorf("sanitising destroyed the message: %q", got.Value)
	}
	if got.Step != msg.Step || got.Status != msg.Status {
		t.Error("sanitising must not alter routing fields")
	}
}

func TestSanitizeWSMessageRedactsPrivateKeyMaterial(t *testing.T) {
	msg := WSMessage{
		Status: "error",
		Error:  "key rejected: -----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEA\n-----END OPENSSH PRIVATE KEY-----",
	}
	got := SanitizeWSMessage(msg)
	if strings.Contains(got.Error, "b3BlbnNzaC1rZXktdjEA") {
		t.Errorf("private key body survived: %q", got.Error)
	}
}

func TestSanitizeWSMessageLeavesCleanMessagesUntouched(t *testing.T) {
	msg := WSMessage{Step: "validation", Status: "success", Value: "Validation passed"}
	if got := SanitizeWSMessage(msg); got != msg {
		t.Errorf("a clean message was altered: %+v", got)
	}
}

// A token in a bearer header must not reach the client either.
func TestSanitizeWSMessageRedactsBearerToken(t *testing.T) {
	msg := WSMessage{Status: "error", Value: "curl failed: Authorization: Bearer abcdef123456"}
	if got := SanitizeWSMessage(msg); strings.Contains(got.Value, "abcdef123456") {
		t.Errorf("bearer token survived: %q", got.Value)
	}
}
