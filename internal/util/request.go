package util

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
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
		if forwarded := strings.TrimSpace(firstHeaderToken(r.Header.Get("X-Forwarded-For"))); forwarded != "" {
			return forwarded
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
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
	if !envFlagEnabled("TRUST_PROXY_HEADERS") {
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
