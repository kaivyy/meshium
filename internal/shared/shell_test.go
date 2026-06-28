package shared

import (
	"strings"
	"testing"
)

func TestShellQuoteSimpleString(t *testing.T) {
	result := ShellQuote("hello")
	if result != "'hello'" {
		t.Errorf("expected 'hello', got %s", result)
	}
}

func TestShellQuoteEmptyString(t *testing.T) {
	result := ShellQuote("")
	if result != "''" {
		t.Errorf("expected '', got %s", result)
	}
}

func TestShellQuoteWithSingleQuote(t *testing.T) {
	result := ShellQuote("hello'world")
	// The result should be: 'hello'\''world'
	if result != "'hello'\\''world'" {
		t.Errorf("expected 'hello'\\''world', got %s", result)
	}
}

func TestShellQuoteWithMultipleSingleQuotes(t *testing.T) {
	result := ShellQuote("it's a 'test'")
	// Each ' becomes '\'' so: 'it'\''s a '\''test'\'''
	if result != "'it'\\''s a '\\''test'\\'''" {
		t.Errorf("expected proper escaping, got %s", result)
	}
}

func TestShellQuoteWithSpecialChars(t *testing.T) {
	// Characters that are dangerous in shell: ; & | $ ` ( ) < > \n
	result := ShellQuote("hello; rm -rf /")
	if !strings.HasPrefix(result, "'") || !strings.HasSuffix(result, "'") {
		t.Errorf("result should be wrapped in single quotes, got %s", result)
	}
	// The dangerous content should be inside single quotes, not interpreted
	if !strings.Contains(result, "hello; rm -rf /") {
		t.Errorf("content should be preserved inside quotes, got %s", result)
	}
}

func TestShellQuoteWithBacktick(t *testing.T) {
	result := ShellQuote("`whoami`")
	if !strings.Contains(result, "`whoami`") {
		t.Errorf("backtick content should be preserved, got %s", result)
	}
}

func TestShellQuoteWithDollarSign(t *testing.T) {
	result := ShellQuote("$HOME")
	if !strings.Contains(result, "$HOME") {
		t.Errorf("dollar sign content should be preserved, got %s", result)
	}
}

func TestShellQuoteArgs(t *testing.T) {
	result := ShellQuoteArgs([]string{"nginx", "redis", "postgres"})
	if result != "'nginx' 'redis' 'postgres'" {
		t.Errorf("expected 'nginx' 'redis' 'postgres', got %s", result)
	}
}

func TestShellQuoteArgsEmpty(t *testing.T) {
	result := ShellQuoteArgs([]string{})
	if result != "" {
		t.Errorf("expected empty string, got %s", result)
	}
}

func TestShellQuoteArgsWithInjection(t *testing.T) {
	result := ShellQuoteArgs([]string{"nginx; rm -rf /", "redis"})
	// Each arg should be individually quoted
	if !strings.Contains(result, "'nginx; rm -rf /'") {
		t.Errorf("injection attempt should be quoted, got %s", result)
	}
}

func TestBase64EncodeForShell(t *testing.T) {
	result := Base64EncodeForShell([]byte("hello world"))
	if !strings.HasPrefix(result, "echo '") {
		t.Errorf("expected echo '<base64>' prefix, got %s", result)
	}
	if !strings.Contains(result, "| base64 -d") {
		t.Errorf("expected base64 decode, got %s", result)
	}
}

func TestBase64DecodeCommand(t *testing.T) {
	result := Base64DecodeCommand("/etc/passwd", []byte("hello world"))
	if !strings.Contains(result, "base64 -d >") {
		t.Errorf("expected base64 decode redirect, got %s", result)
	}
	if !strings.Contains(result, "'/etc/passwd'") {
		t.Errorf("expected quoted path, got %s", result)
	}
}

func TestBase64DecodeCommandWithInjectionInPath(t *testing.T) {
	result := Base64DecodeCommand("/etc/passwd; rm -rf /", []byte("content"))
	// The path should be shell-quoted, preventing injection
	if !strings.Contains(result, "'/etc/passwd; rm -rf /'") {
		t.Errorf("path should be shell-quoted, got %s", result)
	}
}

func TestBase64DecodeCommandWithInjectionInData(t *testing.T) {
	// Data with shell injection attempts should be base64-encoded, not interpreted
	malicious := []byte("'; rm -rf /; '")
	result := Base64DecodeCommand("/etc/test", malicious)
	// The base64 encoding should make the injection attempt harmless
	if strings.Contains(result, "rm -rf /") && !strings.Contains(result, "base64") {
		t.Errorf("malicious content should be base64-encoded, got %s", result)
	}
}
