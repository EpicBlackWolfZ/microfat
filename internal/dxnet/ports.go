// Package dxnet provides bounded loopback probes and owned test HTTP servers.
package dxnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"
)

const dialTimeout = 100 * time.Millisecond
const requestTimeout = 2 * time.Second
const maxHeaderBytes = 64 * 1024
const maxPort = 65535

type listenFunc func(context.Context, string, string) (net.Listener, error)
type dialFunc func(context.Context, string, string) (net.Conn, error)

// Occupied checks both IPv4 bindability and localhost reachability. Only a
// successful bind and a refused connection establish a free port; unknown errors fail.
func Occupied(ctx context.Context, value string) (bool, error) {
	var listener net.ListenConfig
	dialer := net.Dialer{Timeout: dialTimeout}
	return occupied(ctx, value, listener.Listen, dialer.DialContext)
}

func occupied(ctx context.Context, value string, listen listenFunc, dial dialFunc) (bool, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > maxPort {
		return false, errors.New("port must be an integer from 1 to 65535")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	canonical := strconv.Itoa(port)
	listener, err := listen(ctx, "tcp4", net.JoinHostPort("127.0.0.1", canonical))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return true, nil
		}
		return false, err
	}
	if err := listener.Close(); err != nil {
		return false, err
	}
	connection, err := dial(ctx, "tcp", net.JoinHostPort("localhost", canonical))
	if err == nil {
		return true, connection.Close()
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return false, nil
	}
	return false, err
}

// Serve owns listener until cancellation. Its one-line readiness record is
// emitted after binding, so test callers never race a free-port reservation.
func Serve(ctx context.Context, listener net.Listener, ready io.Writer) error {
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return errors.Join(err, listener.Close())
	}
	if _, err := fmt.Fprintln(ready, port); err != nil {
		return errors.Join(err, listener.Close())
	}
	server := http.Server{ReadHeaderTimeout: requestTimeout, ReadTimeout: requestTimeout, WriteTimeout: requestTimeout,
		IdleTimeout: requestTimeout, MaxHeaderBytes: maxHeaderBytes,
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })}
	stop := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stop()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}
