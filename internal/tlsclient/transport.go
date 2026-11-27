// Package tlsclient constructs outbound HTTP transports enforcing TLS 1.3 minimum policy.
package tlsclient

import (
	"crypto/tls"
	"net/http"
)

// NewTransport returns a cloned default HTTP transport requiring TLS 1.3 or newer.
func NewTransport() *http.Transport {
	return cloneWithTLS13(http.DefaultTransport)
}

func cloneWithTLS13(rt http.RoundTripper) *http.Transport {
	var transport *http.Transport
	if base, ok := rt.(*http.Transport); ok && base != nil {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13}
		return transport
	}
	cfg := transport.TLSClientConfig.Clone()
	cfg.MinVersion = tls.VersionTLS13
	transport.TLSClientConfig = cfg
	return transport
}
