package slug

import "testing"

func TestSlugify(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"simple two words", "OAuth login", "oauth-login"},
		{"sentence", "add OAuth login to Keep", "add-oauth-login-to-keep"},
		{"special characters", "fix: auth bug #123", "fix-auth-bug-123"},
		{"leading trailing hyphens trimmed", "  --hello--  ", "hello"},
		{"consecutive hyphens collapsed", "hello---world", "hello-world"},
		{"empty string", "", ""},
		{"only special chars", "!@#$%", ""},
		{"preserves slash", "fix/something", "fix/something"},
		{"uppercase mixed", "My GREAT Feature", "my-great-feature"},
		{"numbers only", "123", "123"},
		{"single word", "refactor", "refactor"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Slugify(tc.input)
			if got != tc.expect {
				t.Errorf("Slugify(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}
