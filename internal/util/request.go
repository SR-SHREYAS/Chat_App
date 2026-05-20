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
	trustedProxyCIDRsLoaded      bool
	trustedProxyCIDRs            []*net.IPNet
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
		if _, realIP := parseValidIP(r.Header.Get("X-Real-IP")); realIP != "" {
			return realIP
		}
	}

	if remoteIP != "" {
		return remoteIP
	}

	return ""
}

func remoteIPFromAddr(remoteAddr string) string {
	trimmed := strings.TrimSpace(remoteAddr)
	host, _, err := net.SplitHostPort(trimmed)
	if err == nil {
		_, ip := parseValidIP(host)
		return ip
	}
	_, ip := parseValidIP(trimmed)
	return ip
}

func trustProxyHeadersForRemote(remoteIP string) bool {
	if !trustProxyHeadersEnabled() {
		return false
	}
	if remoteIP == "" {
		return false
	}
	ip, _ := parseValidIP(remoteIP)
	if ip == nil {
		return false
	}

	if cidrs := trustedProxyCIDRsFromEnv(); len(cidrs) > 0 {
		for _, cidr := range cidrs {
			if cidr.Contains(ip) {
				return true
			}
		}
		return false
	}

	// Default to local/private peers unless TRUSTED_PROXY_CIDRS is configured.
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

func trustedProxyCIDRsFromEnv() []*net.IPNet {
	trustProxyHeadersMu.RLock()
	if trustedProxyCIDRsLoaded {
		cidrs := trustedProxyCIDRs
		trustProxyHeadersMu.RUnlock()
		return cidrs
	}
	trustProxyHeadersMu.RUnlock()

	trustProxyHeadersMu.Lock()
	defer trustProxyHeadersMu.Unlock()
	if !trustedProxyCIDRsLoaded {
		trustedProxyCIDRs = parseTrustedProxyCIDRs(os.Getenv("TRUSTED_PROXY_CIDRS"))
		trustedProxyCIDRsLoaded = true
	}
	return trustedProxyCIDRs
}

func parseTrustedProxyCIDRs(raw string) []*net.IPNet {
	parts := strings.Split(raw, ",")
	cidrs := make([]*net.IPNet, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}

		if strings.Contains(value, "/") {
			if _, cidr, err := net.ParseCIDR(value); err == nil {
				cidrs = append(cidrs, cidr)
			}
			continue
		}

		if ip := net.ParseIP(value); ip != nil {
			maskBits := 128
			if ip.To4() != nil {
				maskBits = 32
			}
			cidrs = append(cidrs, &net.IPNet{
				IP:   ip,
				Mask: net.CIDRMask(maskBits, maskBits),
			})
		}
	}
	return cidrs
}

func parseValidIP(raw string) (net.IP, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, ""
	}
	if ip := net.ParseIP(trimmed); ip != nil {
		return ip, ip.String()
	}
	return nil, ""
}

func clientIPFromXForwardedFor(header string) string {
	if strings.TrimSpace(header) == "" {
		return ""
	}

	parts := strings.Split(header, ",")
	for _, part := range parts {
		parsed, ip := parseValidIP(part)
		if ip == "" {
			continue
		}

		// X-Forwarded-For is conventionally "client, proxy1, proxy2".
		if parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() {
			continue
		}
		return ip
	}

	return ""
}
