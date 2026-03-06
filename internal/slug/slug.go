package slug

import (
	"regexp"
	"strings"
)

// nonAlphaNum matches any character that is not a lowercase letter, digit, or forward slash.
var nonAlphaNum = regexp.MustCompile(`[^a-z0-9/]+`)

// multiHyphen matches two or more consecutive hyphens.
var multiHyphen = regexp.MustCompile(`-{2,}`)

// Slugify converts arbitrary text into a URL/branch-safe slug.
//   - Lowercases the input
//   - Replaces spaces and special characters with hyphens
//   - Collapses consecutive hyphens
//   - Trims leading/trailing hyphens
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}

	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = multiHyphen.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	return s
}
