package system

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingWriter struct {
	calls  int
	failAt int
}

func (w *failingWriter) Write(data []byte) (int, error) {
	w.calls++
	if w.calls >= w.failAt {
		return 0, errors.New("output failure")
	}
	return len(data), nil
}

func TestObserverContract(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "phase")
	require.NoError(t, os.WriteFile(file, []byte("startup"), controlMode))
	cfg := ObserverConfig{PID: os.Getpid(), PhaseFile: file, IntervalMS: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	require.NoError(t, Observe(ctx, cfg, &output))
	assert.Contains(t, output.String(), "rss_bytes")
	require.Error(t, Observe(ctx, ObserverConfig{}, io.Discard))
	require.Error(t, Observe(ctx, cfg, &failingWriter{failAt: 1}))
	require.Error(t, Observe(ctx, cfg, &failingWriter{failAt: 2}))
	require.NoError(t, os.WriteFile(file, []byte("invalid"), controlMode))
	require.Error(t, Observe(ctx, cfg, io.Discard))
	require.NoError(t, os.Remove(file))
	require.Error(t, Observe(ctx, cfg, io.Discard))
}

func TestDirectBootstrapFailure(t *testing.T) {
	cpus, err := SupportedAffinity(nil)
	require.NoError(t, err)
	require.Error(t, ExecChild(ChildConfig{Path: missingExecutable, Affinity: cpus}))
	require.Error(t, ExecChild(ChildConfig{Path: missingExecutable, Affinity: []int{MaxCPU - 1}}))
	require.Error(t, ExecChild(ChildConfig{Path: missingExecutable, Procs: []string{"/missing/cgroup.procs"}}))
	_, err = SupportedAffinity([]int{MaxCPU - 1})
	require.Error(t, err)
	s, err := Prepare(Options{CPUPeriodUS: DefaultPeriod, Affinity: []int{MaxCPU - 1}})
	require.NoError(t, err)
	assert.Equal(t, controlUnavailable, s.Controls[0].State)
}
