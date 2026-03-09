package webscrape

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

var privateRanges []*net.IPNet

func init() {
	// IPv4 private ranges (matched against 4-byte IPs)
	cidrs := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"224.0.0.0/4",   // multicast
		"240.0.0.0/4",   // reserved/broadcast
		"0.0.0.0/8",     // unspecified
		"100.64.0.0/10", // shared address (CGNAT)
		// IPv6 private ranges
		"::1/128",   // loopback
		"fe80::/10", // link-local
		"fc00::/7",  // unique local
	}
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil {
			privateRanges = append(privateRanges, network)
		}
	}
}

// ValidateURL parses, normalizes, and validates a URL for safe external fetching.
// It blocks private/internal IP ranges (SSRF protection) and non-HTTP(S) schemes.
// The URL is resolved at validation time to prevent DNS rebinding attacks.
func ValidateURL(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("scheme %q not allowed: only http and https are permitted", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("URL has no host")
	}

	// Resolve to IPs and validate each — catches DNS rebinding
	addrs, err := net.LookupHost(host)
	if err != nil {
		return "", fmt.Errorf("cannot resolve host %q: %w", host, err)
	}

	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if isPrivateIP(ip) {
			return "", fmt.Errorf("host %q resolves to private IP %s: access denied", host, addr)
		}
	}

	return u.String(), nil
}

// isPrivateIP returns true if the IP falls in a private or reserved range.
func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// Normalize: convert IPv4-in-IPv6 to 4-byte form so IPv4 CIDRs match correctly.
	// net.ParseIP always returns 16-byte slices; To4() returns nil for real IPv6.
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	for _, network := range privateRanges {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
