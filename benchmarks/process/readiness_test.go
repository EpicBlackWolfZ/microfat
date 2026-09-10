package process

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFirstLine(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, script := range []string{"printf 'ready\\n'; sleep 0.1", "printf 'rea'; sleep 0.01; printf 'dy\\n'", "printf 'ready\\n'"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		child, err := Start(ctx, Spec{Path: "/bin/sh", Args: []string{"-c", script}})
		require.NoError(t, err)
		line, at, err := child.FirstLine(ctx)
		require.NoError(t, err)
		assert.Equal(t, "ready", string(line))
		assert.False(t, at.IsZero())
		require.NoError(t, child.Wait())
		line, again, err := child.FirstLine(ctx)
		require.NoError(t, err)
		assert.Equal(t, "ready", string(line))
		assert.Equal(t, at, again)
		cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	child, err := Start(ctx, Spec{Path: "/bin/sh", Args: []string{"-c", "printf partial"}})
	require.NoError(t, err)
	_, _, err = child.FirstLine(ctx)
	require.ErrorContains(t, err, "before readiness")
	child, err = Start(ctx, Spec{Path: "/bin/sh", Args: []string{"-c", "sleep 10"}})
	require.NoError(t, err)
	cancel()
	_, _, err = child.FirstLine(ctx)
	require.Error(t, err)
	_ = child.Wait()
}
