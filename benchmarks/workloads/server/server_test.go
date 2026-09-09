package server

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type errorIO struct{}

func (errorIO) Read([]byte) (int, error)  { return 0, errors.New("read failure") }
func (errorIO) Write([]byte) (int, error) { return 0, errors.New("write failure") }
func (errorIO) Close() error              { return nil }

func TestDeterministicHandlers(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	handler, err := Handler(cfg)
	require.NoError(t, err)
	for _, endpoint := range []string{"mixed", "cpu", "memory", "healthz", "readyz", "diagnostics"} {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			var previous string
			for range 2 {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/"+endpoint, nil))
				require.Equal(t, http.StatusOK, response.Code)
				if previous != "" {
					assert.Equal(t, previous, response.Body.String())
				}
				previous = response.Body.String()
			}
		})
	}
	var group sync.WaitGroup
	for range DefaultConcurrency {
		group.Go(func() {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mixed", strings.NewReader(`{"data":"hello","seed":2}`)))
			assert.Equal(t, http.StatusOK, recorder.Code)
		})
	}
	group.Wait()
}

func TestWorkloadErrors(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*Config){
		"public":          func(c *Config) { c.Address = "0.0.0.0:0" },
		"invalid address": func(c *Config) { c.Address = "bad" },
		"payload":         func(c *Config) { c.PayloadBytes = MaxPayload + 1 },
		"iterations":      func(c *Config) { c.Iterations = 0 },
		"concurrency":     func(c *Config) { c.Concurrency = MaxConcurrency + 1 },
	} {
		t.Run(name, func(t *testing.T) { c := Defaults(); mutate(&c); _, err := Handler(c); require.Error(t, err) })
	}
	handler, err := Handler(Defaults())
	require.NoError(t, err)
	for name, request := range map[string]*http.Request{
		"invalid JSON": httptest.NewRequest(http.MethodPost, "/mixed", strings.NewReader("{")),
		"body limit":   httptest.NewRequest(http.MethodPost, "/mixed", strings.NewReader(strings.Repeat("x", DefaultPayload+1))),
		"read failure": httptest.NewRequest(http.MethodPost, "/mixed", errorIO{}),
		"method":       httptest.NewRequest(http.MethodDelete, "/cpu", nil),
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRecorder()
			handler.ServeHTTP(r, request)
			assert.GreaterOrEqual(t, r.Code, http.StatusBadRequest)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = execute(httptest.NewRequest(http.MethodGet, "/cpu", nil).WithContext(ctx), Defaults(), "cpu")
	require.Error(t, err)
}

type blockingReader struct {
	entered chan struct{}
	resume  chan struct{}
}

func (r blockingReader) Read([]byte) (int, error) { close(r.entered); <-r.resume; return 0, io.EOF }

func TestConcurrencyBound(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Concurrency = 1
	handler, err := Handler(cfg)
	require.NoError(t, err)
	reader := blockingReader{entered: make(chan struct{}), resume: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mixed", reader))
	}()
	<-reader.entered
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/cpu", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	close(reader.resume)
	<-done
}

func TestServerLifecycle(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, Defaults(), writer) }()
	var message map[string]string
	// Read a single newline-delimited handshake without waiting for writer EOF.
	var line bytes.Buffer
	buffer := make([]byte, 1)
	for {
		_, err := reader.Read(buffer)
		require.NoError(t, err)
		if buffer[0] == '\n' {
			break
		}
		line.WriteByte(buffer[0])
	}
	require.NoError(t, json.Unmarshal(line.Bytes(), &message))
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(message["url"] + "/readyz")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusOK, response.StatusCode)
	cancel()
	require.NoError(t, <-done)
	require.NoError(t, reader.Close())
	require.NoError(t, writer.Close())
}

func TestServeAndRunFailures(t *testing.T) {
	t.Setenv("MICROFAT_AUTOTUNE", "0")
	require.Error(t, Serve(context.Background(), Config{}, io.Discard))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	cfg := Defaults()
	cfg.Address = listener.Addr().String()
	require.Error(t, Serve(context.Background(), cfg, io.Discard))
	require.Error(t, Serve(context.Background(), Defaults(), errorIO{}))
	for _, args := range [][]string{{"-unknown"}, {"unexpected"}, {"-iterations", "-1"}} {
		require.Error(t, Run(context.Background(), args, io.Discard, io.Discard))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, Run(ctx, nil, io.Discard, io.Discard))
}
