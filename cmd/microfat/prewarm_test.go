package main

import (
	"bytes"
	json "encoding/json/v2"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cache"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePrewarmLimit(t *testing.T) {
	t.Parallel()
	opts, err := parsePrewarmLimit("ignored", false)
	require.NoError(t, err)
	assert.Nil(t, opts.MaxPayloadBytes)
	for _, raw := range []string{"0", "0001", "18446744073709551615"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			opts, err := parsePrewarmLimit(raw, true)
			require.NoError(t, err)
			require.NotNil(t, opts.MaxPayloadBytes)
			want, err := strconv.ParseUint(raw, 10, 64)
			require.NoError(t, err)
			assert.Equal(t, want, *opts.MaxPayloadBytes)
		})
	}
	for _, raw := range []string{"", "-1", "+1", " 1", "1\n", "1.0", "1e3", "0x10", "1_000", "１２", "18446744073709551616"} {
		t.Run(strconv.QuoteToASCII(raw), func(t *testing.T) {
			t.Parallel()
			cmd := newPrewarmCmd()
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"missing-input", "--max-payload-bytes=" + raw})
			require.ErrorContains(t, cmd.Execute(), "invalid --max-payload-bytes")
		})
	}
}

func TestCLIPrewarmPlanningAndAdmission(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stub, payload := filepath.Join(dir, "stub"), filepath.Join(dir, "payload")
	require.NoError(t, os.WriteFile(stub, []byte("stub"), 0o700))
	data := []byte("selected logical payload")
	require.NoError(t, os.WriteFile(payload, data, 0o700))
	source := filepath.Join(dir, "packed")
	_, err := pack.Pack(pack.Options{
		StubPath: stub, OutputPath: source, TargetArch: testArchAMD64, SkipELFValidation: true,
		Variants: map[string]string{"v1": payload, "v2": payload},
	})
	require.NoError(t, err)
	cacheDir := filepath.Join(dir, "missing-cache")
	execute := func(flags ...string) (string, error) {
		t.Helper()
		cmd := newPrewarmCmd()
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(append([]string{source, "--all", "--cache-dir", cacheDir}, flags...))
		err := cmd.Execute()
		return out.String(), err
	}
	out, err := execute("--plan", "--json")
	require.NoError(t, err)
	var report struct {
		Event string `json:"event"`
		pack.PrewarmPlan
	}
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	assert.Equal(t, "prewarm_plan", report.Event)
	assert.Equal(t, uint64(len(data)), report.UniquePayloadBytes)
	assert.Equal(t, report.UniquePayloadBytes, report.NeedsMaterializationBytes)
	assert.Len(t, report.Entries, 2)
	assert.NoDirExists(t, cacheDir)
	out, err = execute("--plan")
	require.NoError(t, err)
	assert.Contains(t, out, "Unique selected payload bytes:")
	assert.Contains(t, out, "Read-only snapshot")
	for _, mode := range [][]string{{}, {"--plan"}, {"--verify"}} {
		_, err := execute(append(mode, "--max-payload-bytes=0")...)
		require.ErrorIs(t, err, pack.ErrPrewarmPayloadLimit)
		assert.NoDirExists(t, cacheDir)
	}
	_, err = execute("--max-payload-bytes=" + strconv.Itoa(len(data)))
	require.NoError(t, err)
	_, err = execute("--plan", "--verify")
	require.ErrorContains(t, err, "none of the others")
	_, err = execute("--level=v1")
	require.ErrorContains(t, err, "none of the others")
	_, err = execute("--plan", "--max-payload-bytes="+strconv.FormatUint(math.MaxUint64, 10))
	require.NoError(t, err)
}

type prewarmErrorWriter struct{}

func (prewarmErrorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestPrintPrewarmPlan(t *testing.T) {
	t.Parallel()
	idx := &format.Index{AppName: "test", TargetOS: "linux", TargetArch: testArchAMD64}
	plan := &pack.PrewarmPlan{Entries: []pack.PrewarmPlanEntry{{
		Level: "v1", CacheStatus: cache.ObservationUnsafe, CacheError: "unsafe permissions", DecoderError: "unavailable",
	}}}
	for _, asJSON := range []bool{false, true} {
		var out bytes.Buffer
		require.ErrorContains(t, printPrewarmPlan(&out, idx, plan, asJSON), "unsafe, unavailable, or undecodable")
		assert.Contains(t, out.String(), "unsafe permissions")
		assert.Contains(t, out.String(), "unavailable")
	}
	require.ErrorIs(t, printPrewarmPlan(prewarmErrorWriter{}, idx, plan, true), io.ErrClosedPipe)
	require.ErrorIs(t, printPrewarmPlan(prewarmErrorWriter{}, idx, plan, false), io.ErrClosedPipe)
}
