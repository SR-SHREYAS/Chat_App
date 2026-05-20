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
	trustProxyHeadersOnce sync.Once
	trustProxyHeaders     bool
	trustProxyHeadersMu   sync.RWMutex
	trustProxyHeadersSet  bool
	trustProxyHeadersVal  bool
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
		if forwarded := firstHeaderToken(r.Header.Get("X-Forwarded-For")); forwarded != "" {
			if ip := parseValidIP(forwarded); ip != "" {
				return ip
			}
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

func firstHeaderToken(raw string) string {
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
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
	if trustProxyHeadersSet {
		enabled := trustProxyHeadersVal
		trustProxyHeadersMu.RUnlock()
		return enabled
	}
	trustProxyHeadersMu.RUnlock()

	trustProxyHeadersOnce.Do(func() {
		trustProxyHeaders = envFlagEnabled("TRUST_PROXY_HEADERS")
	})
	return trustProxyHeaders
}

// SetTrustProxyHeadersOverride allows callers (for example tests or boot-time
// configuration wiring) to explicitly control whether forwarded proxy headers
// are trusted by ClientIP.
func SetTrustProxyHeadersOverride(enabled bool) {
	trustProxyHeadersMu.Lock()
	trustProxyHeadersSet = true
	trustProxyHeadersVal = enabled
	trustProxyHeadersMu.Unlock()
}

// ClearTrustProxyHeadersOverride clears any explicit override and returns
// ClientIP trust behavior to environment-based configuration.
func ClearTrustProxyHeadersOverride() {
	trustProxyHeadersMu.Lock()
	trustProxyHeadersSet = false
	trustProxyHeadersVal = false
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
