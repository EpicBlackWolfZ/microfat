package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const probeCommand = "probe-port"

type readyWriter struct{ cancel context.CancelFunc }

func (r readyWriter) Write(data []byte) (int, error) { r.cancel(); return len(data), nil }

type badWriter struct{}

func (badWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestCommands(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"unknown"}, {probeCommand}, {probeCommand, "bad"}, {"serve", "extra"}} {
		require.Error(t, run(context.Background(), args, io.Discard))
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	var out strings.Builder
	require.NoError(t, run(context.Background(), []string{probeCommand, port}, &out))
	require.Equal(t, "occupied\n", out.String())
	require.NoError(t, listener.Close())
	out.Reset()
	require.NoError(t, run(context.Background(), []string{probeCommand, port}, &out))
	require.Contains(t, []string{"free\n", "occupied\n"}, out.String(), "another process may claim a released port")
	out.Reset()
	require.NoError(t, writeState(&out, false))
	require.Equal(t, "free\n", out.String())
	require.Error(t, run(context.Background(), []string{probeCommand, port}, badWriter{}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, run(ctx, []string{"serve"}, readyWriter{cancel}))
	require.Error(t, run(ctx, []string{"serve"}, io.Discard))
}
func TestMain(t *testing.T) {
	if os.Getenv("MICROFAT_DX_NET_HELPER") == "1" {
		os.Args = []string{"dx-net"}
		main()
		return
	}
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMain$")
	cmd.Env = append(os.Environ(), "MICROFAT_DX_NET_HELPER=1")
	output, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "usage: dx-net")
}
