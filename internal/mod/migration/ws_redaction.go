package migration

import "meshium/internal/shared"

// SanitizeWSMessage strips secret material from an outbound progress message.
//
// Progress messages carry raw remote stderr and command text to the browser
// and into the persisted event log. Sanitisation used to be applied at a
// handful of call sites and nowhere else, so any stage that surfaced a failing
// command leaked whatever its arguments held — database passwords, tokens, key
// material. Routing this through one function means a new stage cannot forget
// it, and the redaction rules live in exactly one place (shared.SanitizeString).
//
// Only the human-readable fields are touched; Step and Status drive routing
// and must pass through unchanged.
func SanitizeWSMessage(msg WSMessage) WSMessage {
	if msg.Value != "" {
		msg.Value = shared.SanitizeString(msg.Value)
	}
	if msg.Error != "" {
		msg.Error = shared.SanitizeString(msg.Error)
	}
	return msg
}
