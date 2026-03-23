package util

import (
	"net/http"
	"net/url"
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
