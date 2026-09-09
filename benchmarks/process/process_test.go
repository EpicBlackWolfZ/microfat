package process

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("MICROFAT_TEST_CHILD")
	if mode == "" {
		return
	}
	switch mode {
	case "echo":
		fmt.Fprint(os.Stdout, "output")
		fmt.Fprint(os.Stderr, "diagnostic")
	case "fail":
		os.Exit(1)
	case "wait":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func helper(t *testing.T, mode string) Spec {
	t.Helper()
	path, err := os.Executable()
	require.NoError(t, err)
	return Spec{Path: path, Args: []string{"-test.run=^TestHelperProcess$"}, Env: append(os.Environ(), "MICROFAT_TEST_CHILD="+mode)}
}

func TestProcessLifecycle(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), WaitDelay)
	defer cancel()
	child, err := Start(ctx, helper(t, "echo"))
	require.NoError(t, err)
	require.Positive(t, child.PID())
	require.NoError(t, child.Wait())
	require.NotNil(t, child.State())
	assert.Equal(t, "output", string(child.Stdout()))
	assert.Equal(t, "diagnostic", string(child.Stderr()))
	require.NoError(t, child.Stop(ctx))
	select {
	case <-child.Done():
	default:
		t.Fatal("child not reaped")
	}
	_, _, err = Run(ctx, helper(t, "fail"))
	require.Error(t, err)
	_, _, err = Run(ctx, Spec{Path: "/does-not-exist"})
	require.Error(t, err)
}

func TestCancellationAndOutputLimit(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	child, err := Start(ctx, helper(t, "wait"))
	require.NoError(t, err)
	cancel()
	require.Error(t, child.Wait())
	child, err = Start(context.Background(), helper(t, "wait"))
	require.NoError(t, err)
	stop, stopCancel := context.WithCancel(context.Background())
	stopCancel()
	require.Error(t, child.Stop(stop))
	cancelled := false
	buffer := &Buffer{limit: 1, cancel: func() { cancelled = true }}
	_, err = buffer.Write([]byte("a"))
	require.NoError(t, err)
	assert.NoError(t, buffer.Err())
	_, err = buffer.Write([]byte("b"))
	require.ErrorIs(t, err, ErrOutputLimit)
	assert.True(t, cancelled)
	require.ErrorIs(t, buffer.Err(), ErrOutputLimit)
	assert.Equal(t, []byte("a"), buffer.Bytes())
}
