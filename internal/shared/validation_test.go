package shared

import (
	"testing"
)

func TestValidatePort(t *testing.T) {
	tests := []struct {
		port int
		want bool
	}{
		{0, false},
		{1, true},
		{22, true},
		{80, true},
		{443, true},
		{8080, true},
		{65535, true},
		{65536, false},
		{-1, false},
		{99999, false},
	}
	for _, tt := range tests {
		if got := ValidatePort(tt.port); got != tt.want {
			t.Errorf("ValidatePort(%d) = %v, want %v", tt.port, got, tt.want)
		}
	}
}

func TestValidateRequiredString(t *testing.T) {
	if !ValidateRequiredString("hello", 255) {
		t.Error("non-empty string within max length should be valid")
	}
	if ValidateRequiredString("", 255) {
		t.Error("empty string should be invalid")
	}
	if ValidateRequiredString("   ", 255) {
		t.Error("whitespace-only string should be invalid")
	}
	if ValidateRequiredString("hello", 3) {
		t.Error("string exceeding max length should be invalid")
	}
}

func TestValidateOptionalString(t *testing.T) {
	if !ValidateOptionalString("", 255) {
		t.Error("empty string should be valid for optional fields")
	}
	if !ValidateOptionalString("hello", 255) {
		t.Error("non-empty string within max length should be valid")
	}
	if ValidateOptionalString("hello", 3) {
		t.Error("string exceeding max length should be invalid")
	}
}

func TestValidateTags(t *testing.T) {
	if !ValidateTags(nil) {
		t.Error("nil tags should be valid")
	}
	if !ValidateTags([]string{"web", "prod"}) {
		t.Error("small number of short tags should be valid")
	}
	if !ValidateTags([]string{}) {
		t.Error("empty tags slice should be valid")
	}
	// Too many tags
	tooMany := make([]string, MaxTagsCount+1)
	for i := range tooMany {
		tooMany[i] = "tag"
	}
	if ValidateTags(tooMany) {
		t.Error("too many tags should be invalid")
	}
	// Tag too long
	longTag := make([]string, 1)
	longTag[0] = string(make([]byte, MaxTagLength+1))
	if ValidateTags(longTag) {
		t.Error("tag exceeding max length should be invalid")
	}
}
