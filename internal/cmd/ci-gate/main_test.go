package main

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/EpicBlackWolfZ/microfat/internal/cigate"
	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	t.Parallel()
	jobs := map[string]cigate.Job{}
	for _, name := range cigate.RequiredJobs() {
		jobs[name] = cigate.Job{Result: cigate.Success}
	}
	jobs["lint"] = cigate.Job{Result: cigate.Success, Outputs: map[string]string{"run-code-checks": "true"}}
	data, err := json.Marshal(jobs)
	require.NoError(t, err)
	var output bytes.Buffer
	require.Zero(t, run([]string{"pull_request"}, bytes.NewReader(data), &output))
	require.Contains(t, output.String(), "All required CI stages")

	pushJobs := map[string]cigate.Job{}
	baseline := cigate.PushBaselineJobs()
	for _, name := range cigate.RequiredJobs() {
		result := cigate.Skipped
		if slices.Contains(baseline, name) {
			result = cigate.Success
		}
		pushJobs[name] = cigate.Job{Result: result}
	}
	pushJobs["lint"] = cigate.Job{Result: cigate.Success, Outputs: map[string]string{"run-code-checks": "true"}}
	pushData, err := json.Marshal(pushJobs)
	require.NoError(t, err)
	output.Reset()
	require.Zero(t, run([]string{"push"}, bytes.NewReader(pushData), &output))
	require.Contains(t, output.String(), "All required CI stages")
	require.Equal(t, 1, run([]string{"push"}, bytes.NewReader(data), io.Discard))

	require.Equal(t, 1, run(nil, strings.NewReader(""), io.Discard))
	require.Equal(t, 1, run([]string{"pull_request"}, strings.NewReader("{}"), io.Discard))
	require.Equal(t, 1, run([]string{"pull_request"}, iotest.ErrReader(io.ErrUnexpectedEOF), io.Discard))
	require.Equal(t, 1, run([]string{"pull_request"}, strings.NewReader(strings.Repeat(" ", cigate.MaxNeedsBytes+1)), io.Discard))
}
