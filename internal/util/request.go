package util

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

var (
	trustProxyHeadersMu          sync.RWMutex
	trustProxyHeadersLoaded      bool
	trustProxyHeadersEnvValue    bool
	trustProxyHeadersOverrideSet bool
	trustProxyHeadersOverride    bool
)

func CheckSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsedOrigin, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsedOrigin.Host, r.Host)
}

func SanitizeQueryValue(v string, maxLen int) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if maxLen > 0 {
		runes := []rune(v)
		if len(runes) > maxLen {
			v = string(runes[:maxLen])
		}
	}
	return v
}

func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}

	remoteIP := remoteIPFromAddr(r.RemoteAddr)
	if trustProxyHeadersForRemote(remoteIP) {
		if ip := clientIPFromXForwardedFor(r.Header.Get("X-Forwarded-For")); ip != "" {
			return ip
		}
		if realIP := parseValidIP(r.Header.Get("X-Real-IP")); realIP != "" {
			return realIP
		}
	}

	if remoteIP != "" {
		return remoteIP
	}

	return strings.TrimSpace(r.RemoteAddr)
}

func remoteIPFromAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(remoteAddr)
}

func trustProxyHeadersForRemote(remoteIP string) bool {
	if !trustProxyHeadersEnabled() {
		return false
	}
	if remoteIP == "" {
		return false
	}
	ip := net.ParseIP(remoteIP)
	if ip == nil {
		return false
	}

	// Only trust forwarded headers when the direct peer is local/private.
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func envFlagEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func trustProxyHeadersEnabled() bool {
	trustProxyHeadersMu.RLock()
	if trustProxyHeadersOverrideSet {
		enabled := trustProxyHeadersOverride
		trustProxyHeadersMu.RUnlock()
		return enabled
	}
	if trustProxyHeadersLoaded {
		enabled := trustProxyHeadersEnvValue
		trustProxyHeadersMu.RUnlock()
		return enabled
	}
	trustProxyHeadersMu.RUnlock()

	trustProxyHeadersMu.Lock()
	defer trustProxyHeadersMu.Unlock()
	if trustProxyHeadersOverrideSet {
		return trustProxyHeadersOverride
	}
	if !trustProxyHeadersLoaded {
		trustProxyHeadersEnvValue = envFlagEnabled("TRUST_PROXY_HEADERS")
		trustProxyHeadersLoaded = true
	}
	return trustProxyHeadersEnvValue
}

// SetTrustProxyHeadersOverride allows callers (for example tests or boot-time
// configuration wiring) to explicitly control whether forwarded proxy headers
// are trusted by ClientIP.
func SetTrustProxyHeadersOverride(enabled bool) {
	trustProxyHeadersMu.Lock()
	trustProxyHeadersOverrideSet = true
	trustProxyHeadersOverride = enabled
	trustProxyHeadersMu.Unlock()
}

// ClearTrustProxyHeadersOverride clears any explicit override and returns
// ClientIP trust behavior to environment-based configuration.
func ClearTrustProxyHeadersOverride() {
	trustProxyHeadersMu.Lock()
	trustProxyHeadersOverrideSet = false
	trustProxyHeadersOverride = false
	trustProxyHeadersMu.Unlock()
}

func parseValidIP(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if ip := net.ParseIP(trimmed); ip != nil {
		return trimmed
	}
	return ""
}

func clientIPFromXForwardedFor(header string) string {
	if strings.TrimSpace(header) == "" {
		return ""
	}

	parts := strings.Split(header, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := parseValidIP(parts[i])
		if ip == "" {
			continue
		}
		parsed := net.ParseIP(ip)
		if parsed == nil {
			continue
		}

		// Prefer the first public address from the trusted chain.
		if parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() {
			continue
		}
		return ip
	}

	return ""
}
