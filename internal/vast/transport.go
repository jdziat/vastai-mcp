package vast

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// NewPinnedTransport builds an *http.Transport whose proxy settings and root
// CA pool are captured NOW. Call it before any untrusted input (such as a
// .env file) can touch the environment: net/http reads proxy variables once
// behind a sync.Once, and crypto/x509 caches the system pool the same way, so
// warming both here means later changes to HTTPS_PROXY / SSL_CERT_FILE have no
// effect on this client.
//
// The tunings mirror http.DefaultTransport so nothing is lost by not using it.
//
// It fails closed: if the system pool cannot be loaded there is nothing to pin
// and the caller must not fall back to lazily resolved roots.
func NewPinnedTransport(base string) (*http.Transport, error) {
	// Warm the proxy cache.
	if u, err := url.Parse(base); err == nil {
		_, _ = http.ProxyFromEnvironment(&http.Request{URL: u})
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system CA pool: %w", err)
	}
	if pool == nil {
		return nil, errors.New("system CA pool is empty")
	}
	tlsCfg := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsCfg,
	}, nil
}
