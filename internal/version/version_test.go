package version

import (
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	v := Version()
	if v == "" {
		t.Error("Version() should not return empty string")
	}
	
	// Should contain semantic version pattern
	if !strings.Contains(v, ".") {
		t.Error("Version should contain dots for semantic versioning")
	}
}

func TestVersionInfo(t *testing.T) {
	info := Info()
	
	if info.Version == "" {
		t.Error("Info().Version should not be empty")
	}
	
	if info.GitCommit == "" {
		info.GitCommit = "unknown" // acceptable default
	}
	
	if info.BuildTime == "" {
		info.BuildTime = "unknown" // acceptable default
	}
}

func TestFullVersion(t *testing.T) {
	full := FullVersion()
	
	if full == "" {
		t.Error("FullVersion() should not return empty string")
	}
	
	// Should contain version
	if !strings.Contains(full, Version()) {
		t.Error("FullVersion should contain base version")
	}
}

func TestUserAgent(t *testing.T) {
	ua := UserAgent()
	
	if ua == "" {
		t.Error("UserAgent() should not return empty string")
	}
	
	// Should contain virgil and version
	if !strings.Contains(strings.ToLower(ua), "virgil") {
		t.Error("UserAgent should contain 'virgil'")
	}
	
	if !strings.Contains(ua, Version()) {
		t.Error("UserAgent should contain version")
	}
}