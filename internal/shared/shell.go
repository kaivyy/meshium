package shared

import (
	"encoding/base64"
	"strings"
)

// ShellQuote wraps a string in single quotes for safe use in shell commands.
// It escapes any embedded single quotes by replacing ' with '\''.
// This is the standard POSIX shell escaping technique.
//
// Example: ShellQuote("hello'world") returns "'hello'\\''world'"
func ShellQuote(s string) string {
	// Replace single quotes with the '\'' escape sequence
	escaped := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + escaped + "'"
}

// ShellQuoteArgs quotes each argument and joins them with spaces.
// This is useful for constructing command argument lists from untrusted input.
//
// Example: ShellQuoteArgs([]string{"nginx", "redis"}) returns "'nginx' 'redis'"
func ShellQuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = ShellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

// Base64EncodeForShell base64-encodes data and returns a shell command fragment
// that decodes it. This is used to safely pipe arbitrary binary data into
// shell commands without injection risk.
//
// Example: Base64EncodeForShell("hello") returns "echo 'aGVsbG8=' | base64 -d"
func Base64EncodeForShell(data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	return "echo '" + encoded + "' | base64 -d"
}

// Base64DecodeCommand returns a shell command that writes base64-decoded data
// to a file. This eliminates shell injection risk when writing file contents
// to remote servers.
//
// Example: Base64DecodeCommand("/etc/passwd", content) returns
// "echo '<base64>' | base64 -d > /etc/passwd"
func Base64DecodeCommand(path string, data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	return "echo '" + encoded + "' | base64 -d > " + ShellQuote(path)
}
