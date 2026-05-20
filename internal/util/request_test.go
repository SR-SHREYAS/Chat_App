package util

import (
	"net/http"
	"testing"
)

func TestClientIP_TrustDisabledIgnoresProxyHeaders(t *testing.T) {
	SetTrustProxyHeadersOverride(false)
	defer ClearTrustProxyHeadersOverride()

	headers := make(http.Header)
	headers.Set("X-Forwarded-For", "198.51.100.20")
	headers.Set("X-Real-IP", "198.51.100.21")

	req := &http.Request{
		RemoteAddr: "10.0.0.5:1234",
		Header:     headers,
	}

	if got := ClientIP(req); got != "10.0.0.5" {
		t.Fatalf("expected remote ip when trust disabled, got %q", got)
	}
}

func TestClientIP_UsesPublicXForwardedForHopWhenTrusted(t *testing.T) {
	SetTrustProxyHeadersOverride(true)
	defer ClearTrustProxyHeadersOverride()

	headers := make(http.Header)
	headers.Set("X-Forwarded-For", "10.1.1.10, 198.51.100.42")

	req := &http.Request{
		RemoteAddr: "127.0.0.1:1234",
		Header:     headers,
	}

	if got := ClientIP(req); got != "198.51.100.42" {
		t.Fatalf("expected public forwarded ip, got %q", got)
	}
}

func TestClientIP_UsesLeftMostPublicXForwardedForHop(t *testing.T) {
	SetTrustProxyHeadersOverride(true)
	defer ClearTrustProxyHeadersOverride()

	headers := make(http.Header)
	headers.Set("X-Forwarded-For", "9.9.9.9, 8.8.4.4")

	req := &http.Request{
		RemoteAddr: "127.0.0.1:1234",
		Header:     headers,
	}

	if got := ClientIP(req); got != "9.9.9.9" {
		t.Fatalf("expected left-most public forwarded ip, got %q", got)
	}
}

func TestClientIP_FallsBackToXRealIPWhenForwardedChainIsPrivate(t *testing.T) {
	SetTrustProxyHeadersOverride(true)
	defer ClearTrustProxyHeadersOverride()

	headers := make(http.Header)
	headers.Set("X-Forwarded-For", "10.1.1.10, 127.0.0.1")
	headers.Set("X-Real-IP", "8.8.4.4")

	req := &http.Request{
		RemoteAddr: "127.0.0.1:1234",
		Header:     headers,
	}

	if got := ClientIP(req); got != "8.8.4.4" {
		t.Fatalf("expected X-Real-IP fallback, got %q", got)
	}
}

func TestClientIP_IgnoresProxyHeadersForUntrustedRemote(t *testing.T) {
	SetTrustProxyHeadersOverride(true)
	defer ClearTrustProxyHeadersOverride()

	headers := make(http.Header)
	headers.Set("X-Forwarded-For", "198.51.100.42")
	headers.Set("X-Real-IP", "203.0.113.9")

	req := &http.Request{
		RemoteAddr: "8.8.8.8:1234",
		Header:     headers,
	}

	if got := ClientIP(req); got != "8.8.8.8" {
		t.Fatalf("expected direct remote ip for untrusted peer, got %q", got)
	}
}

func TestClientIP_TrustsConfiguredPublicProxyCIDR(t *testing.T) {
	SetTrustProxyHeadersOverride(true)
	defer ClearTrustProxyHeadersOverride()
	resetTrustedProxyCIDRsForTest(t)
	t.Setenv("TRUSTED_PROXY_CIDRS", "8.8.8.8/32")

	headers := make(http.Header)
	headers.Set("X-Forwarded-For", "9.9.9.9")

	req := &http.Request{
		RemoteAddr: "8.8.8.8:1234",
		Header:     headers,
	}

	if got := ClientIP(req); got != "9.9.9.9" {
		t.Fatalf("expected forwarded ip from configured public proxy, got %q", got)
	}
}

func resetTrustedProxyCIDRsForTest(t *testing.T) {
	t.Helper()

	trustProxyHeadersMu.Lock()
	previousLoaded := trustedProxyCIDRsLoaded
	previousCIDRs := trustedProxyCIDRs
	trustedProxyCIDRsLoaded = false
	trustedProxyCIDRs = nil
	trustProxyHeadersMu.Unlock()

	t.Cleanup(func() {
		trustProxyHeadersMu.Lock()
		trustedProxyCIDRsLoaded = previousLoaded
		trustedProxyCIDRs = previousCIDRs
		trustProxyHeadersMu.Unlock()
	})
}
