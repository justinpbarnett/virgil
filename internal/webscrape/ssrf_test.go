package webscrape

import (
	"net"
	"testing"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		// Valid public URLs
		{name: "https public", url: "https://example.com", wantErr: false},
		{name: "http public", url: "http://example.com", wantErr: false},

		// Invalid schemes
		{name: "ftp scheme", url: "ftp://example.com/file.txt", wantErr: true},
		{name: "file scheme", url: "file:///etc/passwd", wantErr: true},
		{name: "javascript scheme", url: "javascript:alert(1)", wantErr: true},
		{name: "data scheme", url: "data:text/html,<h1>test</h1>", wantErr: true},

		// Loopback
		{name: "localhost", url: "http://localhost/", wantErr: true},
		{name: "127.0.0.1", url: "http://127.0.0.1/", wantErr: true},
		{name: "127.0.0.255", url: "http://127.0.0.255/path", wantErr: true},
		{name: "ipv6 loopback", url: "http://[::1]/", wantErr: true},

		// RFC 1918 private ranges
		{name: "10.x.x.x", url: "http://10.0.0.1/", wantErr: true},
		{name: "10.255.255.255", url: "http://10.255.255.255/", wantErr: true},
		{name: "172.16.x.x", url: "http://172.16.0.1/", wantErr: true},
		{name: "172.31.x.x", url: "http://172.31.255.255/", wantErr: true},
		{name: "192.168.x.x", url: "http://192.168.1.1/", wantErr: true},
		{name: "192.168.255.255", url: "http://192.168.255.255/", wantErr: true},

		// Link-local (cloud metadata endpoint)
		{name: "169.254.169.254 metadata", url: "http://169.254.169.254/latest/meta-data/", wantErr: true},
		{name: "169.254.0.1 link-local", url: "http://169.254.0.1/", wantErr: true},

		// Malformed
		{name: "empty", url: "", wantErr: true},
		{name: "no host", url: "http://", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateURLNormalization(t *testing.T) {
	got, err := ValidateURL("https://example.com/path?q=1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://example.com/path?q=1" {
		t.Errorf("got %q, want %q", got, "https://example.com/path?q=1")
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip      string
		private bool
	}{
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.0.1", true},
		{"192.168.0.1", true},
		{"127.0.0.1", true},
		{"169.254.1.1", true},
		{"::1", true},
		{"0.0.0.0", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
	}

	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Skipf("cannot parse %q", tt.ip)
		}
		got := isPrivateIP(ip)
		if got != tt.private {
			t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.private)
		}
	}
}
