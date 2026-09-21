package main

import (
	"bytes"
	"encoding/json"
	"io"
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
	require.Equal(t, 1, run(nil, strings.NewReader(""), io.Discard))
	require.Equal(t, 1, run([]string{"pull_request"}, strings.NewReader("{}"), io.Discard))
	require.Equal(t, 1, run([]string{"pull_request"}, iotest.ErrReader(io.ErrUnexpectedEOF), io.Discard))
	require.Equal(t, 1, run([]string{"pull_request"}, strings.NewReader(strings.Repeat(" ", cigate.MaxNeedsBytes+1)), io.Discard))
}
