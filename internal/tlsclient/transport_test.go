package tlsclient

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCloneWithTLS13(t *testing.T) {
	t.Parallel()

	t.Run("default_transport", func(t *testing.T) {
		t.Parallel()
		tr := NewTransport()
		require.NotNil(t, tr)
		require.NotNil(t, tr.TLSClientConfig)
		assert.Equal(t, uint16(tls.VersionTLS13), tr.TLSClientConfig.MinVersion)
		assert.NotSame(t, http.DefaultTransport, tr)
	})

	t.Run("non_http_transport_fallback", func(t *testing.T) {
		t.Parallel()
		tr := cloneWithTLS13(roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, nil
		}))
		require.NotNil(t, tr.TLSClientConfig)
		assert.Equal(t, uint16(tls.VersionTLS13), tr.TLSClientConfig.MinVersion)
	})

	t.Run("clones_existing_tls_config_without_mutation", func(t *testing.T) {
		t.Parallel()
		origCfg := &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: "example.invalid",
		}
		base := &http.Transport{TLSClientConfig: origCfg}
		tr := cloneWithTLS13(base)
		require.NotNil(t, tr.TLSClientConfig)
		assert.NotSame(t, origCfg, tr.TLSClientConfig)
		assert.Equal(t, uint16(tls.VersionTLS13), tr.TLSClientConfig.MinVersion)
		assert.Equal(t, "example.invalid", tr.TLSClientConfig.ServerName)
		assert.Equal(t, uint16(tls.VersionTLS12), origCfg.MinVersion)
	})
}

func testTLSServer(t *testing.T, minVer, maxVer uint16) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	srv.TLS = &tls.Config{
		MinVersion: minVer,
		MaxVersion: maxVer,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func clientWithServerPool(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tr := cloneWithTLS13(&http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    pool,
		},
	})
	return &http.Client{Transport: tr}
}

func TestNewTransportRejectsTLS12AndAcceptsTLS13(t *testing.T) {
	t.Parallel()

	t.Run("rejects_tls12_only_server", func(t *testing.T) {
		t.Parallel()
		srv := testTLSServer(t, tls.VersionTLS12, tls.VersionTLS12)
		client := clientWithServerPool(t, srv)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		require.Error(t, err)
	})

	t.Run("accepts_tls13_server", func(t *testing.T) {
		t.Parallel()
		srv := testTLSServer(t, tls.VersionTLS13, tls.VersionTLS13)
		client := clientWithServerPool(t, srv)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NotNil(t, resp.TLS)
		assert.Equal(t, uint16(tls.VersionTLS13), resp.TLS.Version)
	})
}
