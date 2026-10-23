package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	json "encoding/json/v2"
	"golang.org/x/sys/unix"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("intentional writer failure") }

func TestCLIAnalysisAndInputGuards(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, writeJSON(path, fixtureReport()))
	var stdout, stderr bytes.Buffer
	require.NoError(t, run([]string{testModeFlag, "analyze", testRequestFlag, path}, &stdout, &stderr))
	assert.Contains(t, stdout.String(), "baseline_attempts")
	require.Error(t, run([]string{testModeFlag, "analyze", testRequestFlag, path}, brokenWriter{}, io.Discard))
	for _, args := range [][]string{{}, {"--unknown"}, {testRequestFlag, path, "extra"}, {testModeFlag, testOther, testRequestFlag, path},
		{testModeFlag, "run", testRequestFlag, path}, {testModeFlag, "prepare", testRequestFlag, path},
		{testModeFlag, "run", testRequestFlag, path, testOutputFlag, filepath.Join(t.TempDir(), "bad")},
		{testModeFlag, "prepare", testRequestFlag, path, testOutputFlag, filepath.Join(t.TempDir(), "bad")},
		{testModeFlag, "analyze", testRequestFlag, "/missing"}} {
		require.Error(t, run(args, io.Discard, io.Discard))
	}
	require.NoError(t, os.WriteFile(path, []byte("{"), privateFileMode))
	require.Error(t, run([]string{testModeFlag, "analyze", testRequestFlag, path}, io.Discard, io.Discard))
	_, err := readJSON(filepath.Dir(path))
	require.Error(t, err)
	fifo := filepath.Join(t.TempDir(), "fifo")
	require.NoError(t, unix.Mkfifo(fifo, privateFileMode))
	_, err = readJSON(fifo)
	require.Error(t, err)
	file, err := os.Create(filepath.Join(t.TempDir(), "large"))
	require.NoError(t, err)
	require.NoError(t, file.Truncate(maximumInput+1))
	require.NoError(t, file.Close())
	_, err = readJSON(file.Name())
	require.Error(t, err)
	require.Error(t, writeJSON(path, math.NaN()))
}

func TestCLIRealProcessCampaign(t *testing.T) {
	in := fixtureRequest()
	directory := t.TempDir()
	tool := filepath.Join(directory, "tool")
	artifact := filepath.Join(directory, "artifact")
	toolScript := "#!/bin/sh\nprintf '%s\\n' '{\"results\":[{\"level\":\"v1\",\"valid\":true}]}'\n"
	require.NoError(t, os.WriteFile(tool, []byte(toolScript), privateDirMode))
	telemetry := successfulRunner(context.Background(), "/artifact", nil, nil).Stderr
	script := "#!/bin/sh\nprintf '%s' ready\nprintf '%s\\n' '" + string(telemetry) + "' >&2\n"
	require.NoError(t, os.WriteFile(artifact, []byte(script), privateDirMode))
	in.Tool = tool
	in.ToolSHA256, _ = hashFile(tool)
	in.Apps[0].Artifact = artifact
	in.Apps[0].ArtifactSHA256, _ = hashFile(artifact)
	in.Apps[0].StdoutSHA256 = textSHA("ready")
	original := producerRevision
	producerRevision = in.SourceRevision
	t.Cleanup(func() { producerRevision = original })
	path := filepath.Join(directory, "request.json")
	require.NoError(t, writeJSON(path, in))
	var output bytes.Buffer
	args := []string{testModeFlag, "run", testRequestFlag, path, testOutputFlag, filepath.Join(directory, "campaign")}
	require.NoError(t, run(args, &output, io.Discard))
	var result report
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	assert.True(t, result.Complete)
	for _, ep := range result.Episodes {
		assert.False(t, ep.Failed)
		assert.Equal(t, in.Scenarios[0].Name, ep.Scenario)
	}
	in.SourceRevision = "wrong"
	require.NoError(t, writeJSON(path, in))
	args = []string{testModeFlag, "run", testRequestFlag, path, testOutputFlag, filepath.Join(directory, "wrong")}
	require.Error(t, run(args, io.Discard, io.Discard))
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", 3)), privateFileMode))
}
