package dxnet

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type readyWriter chan string

func (r readyWriter) Write(data []byte) (int, error) {
	r <- strings.TrimSpace(string(data))
	return len(data), nil
}

type badWriter struct{}

func (badWriter) Write([]byte) (int, error) { return 0, errors.New("ready output failed") }

type listenerCloseError struct{ net.Listener }

func (listenerCloseError) Close() error { return errors.New("close failed") }

type connectionCloseError struct{ net.Conn }

func (connectionCloseError) Close() error { return errors.New("close failed") }

type badAddress struct{ net.Addr }

func (badAddress) String() string { return "invalid" }

type listenerBadAddress struct{ net.Listener }

func (listenerBadAddress) Addr() net.Addr { return badAddress{} }

func listenLoopback(t *testing.T, network, address string) net.Listener {
	t.Helper()
	listener, err := net.Listen(network, address)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}
func TestPortOccupancy(t *testing.T) {
	t.Parallel()
	listener := listenLoopback(t, "tcp4", "127.0.0.1:0")
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	inUse, err := Occupied(context.Background(), port)
	require.NoError(t, err)
	require.True(t, inUse)
	// The probe must never terminate or replace the unrelated occupant.
	connection, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	require.NoError(t, err)
	require.NoError(t, connection.Close())
	require.NoError(t, listener.Close())
	inUse, err = Occupied(context.Background(), port)
	require.NoError(t, err)
	_ = inUse // Another process can claim a released port. The refusal branch is tested with an injected dial result.
	for _, value := range []string{"", "-1", "0", "65536", "not-port", "1; echo bad"} {
		_, err = Occupied(context.Background(), value)
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Occupied(ctx, port)
	require.ErrorIs(t, err, context.Canceled)
}
func TestIPv6OnlyOccupant(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer listener.Close()
	occupied, err := Occupied(context.Background(), strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	require.NoError(t, err)
	require.True(t, occupied)
}
func TestUnknownProbeResultsFailClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	listenError := func(context.Context, string, string) (net.Listener, error) { return nil, syscall.EACCES }
	noDial := func(context.Context, string, string) (net.Conn, error) { t.Fatal("unexpected dial"); return nil, nil }
	_, err := occupied(ctx, "12345", listenError, noDial)
	require.ErrorIs(t, err, syscall.EACCES)
	closeError := func(context.Context, string, string) (net.Listener, error) { return listenerCloseError{}, nil }
	_, err = occupied(ctx, "12345", closeError, noDial)
	require.ErrorContains(t, err, "close failed")
	listener := listenLoopback(t, "tcp4", "127.0.0.1:0")
	listen := func(context.Context, string, string) (net.Listener, error) { return listener, nil }
	timeout := func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	_, err = occupied(ctx, "12345", listen, timeout)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	listener = listenLoopback(t, "tcp4", "127.0.0.1:0")
	refused := func(context.Context, string, string) (net.Conn, error) { return nil, syscall.ECONNREFUSED }
	inUse, err := occupied(ctx, "12345", listen, refused)
	require.NoError(t, err)
	require.False(t, inUse)
	listener = listenLoopback(t, "tcp4", "127.0.0.1:0")
	dialCloseError := func(context.Context, string, string) (net.Conn, error) { return connectionCloseError{}, nil }
	inUse, err = occupied(ctx, "12345", listen, dialCloseError)
	require.True(t, inUse)
	require.ErrorContains(t, err, "close failed")
}
func TestOwnedServerAndCancellation(t *testing.T) {
	t.Parallel()
	listener := listenLoopback(t, "tcp4", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(readyWriter, 1)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listener, ready) }()
	var port string
	select {
	case port = <-ready:
	case <-time.After(time.Second):
		t.Fatal("server did not announce bound port")
	}
	client := http.Client{Timeout: time.Second}
	response, err := client.Get("http://127.0.0.1:" + port)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("server did not close after cancellation")
	}
	connection, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if connection != nil {
		_ = connection.Close()
	}
	require.Error(t, err, "owned listener must close")
}
func TestServerFailureClosesListener(t *testing.T) {
	t.Parallel()
	listener := listenLoopback(t, "tcp4", "127.0.0.1:0")
	require.ErrorContains(t, Serve(context.Background(), listener, badWriter{}), "ready output failed")
	_, err := listener.Accept()
	require.Error(t, err)
	listener = listenLoopback(t, "tcp4", "127.0.0.1:0")
	require.Error(t, Serve(context.Background(), listenerBadAddress{listener}, io.Discard))
	_, err = listener.Accept()
	require.Error(t, err)
	listener = listenLoopback(t, "tcp4", "127.0.0.1:0")
	require.NoError(t, listener.Close())
	require.Error(t, Serve(context.Background(), listener, io.Discard))
}
