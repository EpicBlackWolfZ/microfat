package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessHelper(t *testing.T) {
	role := os.Getenv("RESEARCH380_HELPER")
	if role == "" {
		return
	}
	switch role {
	case "success":
		fmt.Fprint(os.Stdout, "ready")
		fmt.Fprint(os.Stderr, "diagnostic")
	case "fail":
		os.Exit(7)
	case "wait":
		time.Sleep(time.Minute)
	case "large":
		fmt.Fprint(os.Stdout, strings.Repeat("x", maximumOutput+1))
	default:
		os.Exit(9)
	}
	os.Exit(0)
}

func TestExecuteCapturesChildResourcesFailureAndTimeout(t *testing.T) {
	t.Parallel()
	binary, err := os.Executable()
	require.NoError(t, err)
	for _, role := range []string{"success", "fail", "wait", "large"} {
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			timeout := time.Second
			if role == "wait" {
				timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			result := execute(ctx, binary, []string{"-test.run=^TestProcessHelper$"},
				[]string{"RESEARCH380_HELPER=" + role, "GORACE=atexit_sleep_ms=0", "GOCOVERDIR=" + t.TempDir()})
			assert.Positive(t, result.Stats.WallNS)
			switch role {
			case "success":
				assert.Equal(t, 0, result.Stats.ExitCode)
				assert.Equal(t, []byte("ready"), result.Stdout)
				assert.Equal(t, []byte("diagnostic"), result.Stderr)
				assert.Equal(t, textSHA("ready"), result.Stats.StdoutSHA256)
				assert.Positive(t, result.Stats.MaxRSSKiB)
				assert.Positive(t, result.Stats.CPUNS)
			case "fail":
				assert.Equal(t, 7, result.Stats.ExitCode)
				assert.NotEmpty(t, result.Stats.Error)
			case "wait":
				assert.True(t, result.Stats.TimedOut)
				assert.NotEmpty(t, result.Stats.Error)
			case "large":
				assert.Len(t, result.Stdout, maximumOutput)
				assert.Equal(t, int64(maximumOutput+1), result.Stats.StdoutBytes)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := execute(ctx, "/absent", nil, nil)
	assert.NotEmpty(t, result.Stats.Error)
	assert.Equal(t, -1, result.Stats.ExitCode)
	require.ErrorIs(t, killProcessGroup(nil), os.ErrProcessDone)
}

func TestCaptureBoundAndFixedEnvironment(t *testing.T) {
	t.Parallel()
	capture := newCapture()
	data := bytes.Repeat([]byte("x"), maximumOutput+1)
	n, err := capture.Write(data)
	require.NoError(t, err)
	assert.Len(t, capture.prefix.Bytes(), maximumOutput)
	assert.Equal(t, len(data), n)
	n, err = capture.Write([]byte("y"))
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, int64(len(data)+1), capture.count)
	env := processEnvironment("/owned-cache", "v1", 4)
	assert.Equal(t, []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC", "MICROFAT_LOG=json",
		"MICROFAT_EXEC_MODE=auto", "MICROFAT_CACHE_DIR=/owned-cache", "MICROFAT_FORCE_LEVEL=v1", "GOMAXPROCS=4", "GOMEMLIMIT=off"}, env)
}
