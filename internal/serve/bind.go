// Package serve holds transport policy for the HTTP mode.
package serve

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
)

// BindPolicy is the result of CheckBind.
type BindPolicy struct {
	Loopback bool
}

// CheckBind decides whether addr may be served under the given credentials.
//
//   - loopback (127.0.0.1, ::1, localhost) → allowed, token/TLS optional
//   - anything else, including ":8080" / "0.0.0.0" → token required AND
//     TLS required unless insecure is set
func CheckBind(addr string, tokenSet, tlsSet, insecure bool) (BindPolicy, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return BindPolicy{}, fmt.Errorf("invalid -http address %q: %w", addr, err)
	}
	if IsLoopbackHost(host) {
		return BindPolicy{Loopback: true}, nil
	}
	if !tokenSet {
		return BindPolicy{}, fmt.Errorf("-http %s binds a non-loopback interface: set VASTAI_MCP_TOKEN or -http-token-file (this server can spend money and destroy instances)", addr)
	}
	if !tlsSet && !insecure {
		return BindPolicy{}, fmt.Errorf("-http %s binds a non-loopback interface without TLS: pass -tls-cert/-tls-key, or -insecure-http to send the bearer token in plaintext", addr)
	}
	return BindPolicy{}, nil
}

// IsLoopbackHost reports whether host is empty-safe loopback.
func IsLoopbackHost(host string) bool {
	if host == "" {
		return false // ":8080" is all interfaces
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// LoadToken returns the bearer token from VASTAI_MCP_TOKEN or tokenFile.
// Tokens are never accepted as a bare flag (argv is visible in `ps`).
func LoadToken(tokenFile string) (string, error) {
	if v := strings.TrimSpace(os.Getenv("VASTAI_MCP_TOKEN")); v != "" {
		return v, nil
	}
	if tokenFile == "" {
		return "", nil
	}
	b, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", fmt.Errorf("read -http-token-file: %w", err)
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", errors.New("-http-token-file is empty")
	}
	return t, nil
}

// BearerAuth wraps next, requiring `Authorization: Bearer <token>` compared in
// constant time. An empty token disables the check (loopback only).
func BearerAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="vastai-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
