package shared

import (
	"net/http"
	"strings"
)

// MaxRequestBodySize is the maximum allowed size for request bodies (1 MB).
const MaxRequestBodySize = 1 << 20

// MaxFieldNameLength is the maximum length for string fields like name, host, etc.
const MaxFieldNameLength = 255

// MaxDescriptionLength is the maximum length for description fields.
const MaxDescriptionLength = 1024

// MaxTagLength is the maximum length for a single tag.
const MaxTagLength = 64

// MaxTagsCount is the maximum number of tags allowed.
const MaxTagsCount = 20

// ValidatePort returns true if the port is in the valid range (1-65535).
func ValidatePort(port int) bool {
	return port >= 1 && port <= 65535
}

// ValidateStringLength returns true if the string length is within the given bounds.
func ValidateStringLength(s string, minLen, maxLen int) bool {
	length := len(s)
	return length >= minLen && length <= maxLen
}

// ValidateRequiredString returns true if the string is non-empty and within the max length.
func ValidateRequiredString(s string, maxLen int) bool {
	s = strings.TrimSpace(s)
	return s != "" && len(s) <= maxLen
}

// ValidateOptionalString returns true if the string is empty or within the max length.
func ValidateOptionalString(s string, maxLen int) bool {
	return s == "" || len(s) <= maxLen
}

// ValidateTags returns true if the tags slice is valid.
func ValidateTags(tags []string) bool {
	if len(tags) > MaxTagsCount {
		return false
	}
	for _, tag := range tags {
		if len(tag) > MaxTagLength {
			return false
		}
	}
	return true
}

// LimitRequestBody wraps the request body in a LimitedReader to prevent
// excessively large request bodies from causing memory issues.
func LimitRequestBody(r *http.Request) {
	if r.Body != nil {
		r.Body = http.MaxBytesReader(nil, r.Body, MaxRequestBodySize)
	}
}
