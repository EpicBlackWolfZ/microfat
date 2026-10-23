package main

import (
	"bytes"
	"crypto/sha256"
	json "encoding/json/v2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type prewarmReportingFixture struct {
	source      string
	index       *format.Index
	indexOffset int64
	payloads    [][]byte
}

func makePrewarmReportingFixture(t *testing.T, dictionary []byte) prewarmReportingFixture {
	t.Helper()
	fixture := prewarmReportingFixture{
		index: &format.Index{AppName: "reporting-fixture", TargetOS: testOSLinux, TargetArch: testArchAMD64},
		payloads: [][]byte{
			[]byte("first staged application"),
			[]byte("second staged application"),
		},
	}
	var data bytes.Buffer
	if len(dictionary) > 0 {
		fixture.index.DictionarySize = int64(len(dictionary))
		fixture.index.DictionarySHA256 = fmt.Sprintf("%x", sha256.Sum256(dictionary))
		_, err := data.Write(dictionary)
		require.NoError(t, err)
	}
	for i, payload := range fixture.payloads {
		fixture.index.Variants = append(fixture.index.Variants, format.VariantEntry{
			Level: fmt.Sprintf("v%d", i+1), Offset: int64(data.Len()), CompressedSize: int64(len(payload)),
			UncompressedSize: int64(len(payload)), SHA256: fmt.Sprintf("%x", sha256.Sum256(payload)), Compression: codec.AlgorithmNone,
		})
		_, err := data.Write(payload)
		require.NoError(t, err)
	}
	fixture.indexOffset = int64(data.Len())
	_, err := format.WriteIndexAndTrailerWithVersion(&data, fixture.index, fixture.indexOffset, format.FormatVersion2)
	require.NoError(t, err)
	fixture.source = filepath.Join(t.TempDir(), "reporting.fat")
	require.NoError(t, os.WriteFile(fixture.source, data.Bytes(), 0o700))
	return fixture
}

func mutatePrewarmReportingByte(t *testing.T, source string, offset int64) {
	t.Helper()
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	data[offset] ^= 0xff
	require.NoError(t, os.WriteFile(source, data, 0o700))
}

func runPrewarmReportingCommand(t *testing.T, out io.Writer, source, cacheDir string, flags ...string) error {
	t.Helper()
	cmd := newPrewarmCmd()
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(append([]string{source, flagAll, flagCacheDir, cacheDir}, flags...))
	return cmd.Execute()
}

func TestCLIPrewarmReportsSourceFailuresBeforeCacheChanges(t *testing.T) {
	t.Parallel()
	t.Run("malformed manifest", func(t *testing.T) {
		t.Parallel()
		fixture := makePrewarmReportingFixture(t, nil)
		mutatePrewarmReportingByte(t, fixture.source, fixture.indexOffset)
		dir := filepath.Join(t.TempDir(), "missing-cache")
		var out bytes.Buffer
		err := runPrewarmReportingCommand(t, &out, fixture.source, dir, "--plan")
		require.ErrorIs(t, err, format.ErrIndexCorrupted)
		require.ErrorContains(t, err, "reading binary manifest")
		assert.Empty(t, out.String())
		assert.NoDirExists(t, dir)
	})
	t.Run("corrupted shared dictionary", func(t *testing.T) {
		t.Parallel()
		fixture := makePrewarmReportingFixture(t, []byte("shared dictionary bytes"))
		mutatePrewarmReportingByte(t, fixture.source, fixture.index.DictionaryOffset)
		dir := filepath.Join(t.TempDir(), "missing-cache")
		var out bytes.Buffer
		err := runPrewarmReportingCommand(t, &out, fixture.source, dir, "--plan", flagJSON)
		require.ErrorIs(t, err, format.ErrDictionaryCorrupted)
		require.ErrorContains(t, err, "planning prewarm")
		assert.Empty(t, out.String())
		assert.NoDirExists(t, dir)
	})
}

func TestCLIPrewarmReportsCompletedEntriesOnLaterFailure(t *testing.T) {
	t.Parallel()
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", asJSON), func(t *testing.T) {
			t.Parallel()
			fixture := makePrewarmReportingFixture(t, nil)
			mutatePrewarmReportingByte(t, fixture.source, fixture.index.Variants[1].Offset)
			dir := filepath.Join(t.TempDir(), "cache")
			var out bytes.Buffer
			flags := []string{}
			if asJSON {
				flags = append(flags, flagJSON)
			}
			err := runPrewarmReportingCommand(t, &out, fixture.source, dir, flags...)
			require.ErrorIs(t, err, format.ErrPayloadCorrupted)
			require.ErrorContains(t, err, "prewarming variant v2")
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, entries, 1, "failed extraction published a payload or left a temporary file")
			require.Equal(t, fixture.index.Variants[0].SHA256, entries[0].Name())
			cached, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
			require.NoError(t, err)
			require.Equal(t, fixture.payloads[0], cached, "later failure damaged an earlier successful extraction")
			if asJSON {
				var report format.PrewarmTelemetry
				require.NoError(t, json.Unmarshal(out.Bytes(), &report))
				assert.Equal(t, format.EventPrewarm, report.Event)
				assert.Equal(t, dir, report.CacheDir)
				require.Len(t, report.Results, 1)
				assert.Equal(t, fixture.index.Variants[0].SHA256, report.Results[0].SHA256)
				assert.True(t, report.Results[0].Valid)
				assert.Equal(t, format.PrewarmStatusExtracted, report.Results[0].Status)
			} else {
				assert.Contains(t, out.String(), "Prewarming stopped after 1 completed variants.")
				assert.Contains(t, out.String(), fixture.index.Variants[0].SHA256[:12])
				assert.NotContains(t, out.String(), "Cache prewarming complete.")
			}
		})
	}
}

type prewarmNewlineErrorWriter struct{ bytes.Buffer }

func (writer *prewarmNewlineErrorWriter) Write(data []byte) (int, error) {
	if bytes.Equal(data, []byte("\n")) {
		return 0, io.ErrClosedPipe
	}
	return writer.Buffer.Write(data)
}

func TestPrewarmJSONReportsWriterAndTerminatorErrors(t *testing.T) {
	t.Parallel()
	idx := &format.Index{AppName: "writer-test", TargetOS: testOSLinux, TargetArch: testArchAMD64}
	results := []format.PrewarmResult{{Level: "v1", Valid: true, Status: format.PrewarmStatusValid}}
	require.ErrorIs(t, printPrewarmJSON(prewarmErrorWriter{}, idx, results, "cache"), io.ErrClosedPipe)
	newlineWriter := &prewarmNewlineErrorWriter{}
	require.ErrorIs(t, printPrewarmJSON(newlineWriter, idx, results, "cache"), io.ErrClosedPipe)
	var report format.PrewarmTelemetry
	require.NoError(t, json.Unmarshal(newlineWriter.Bytes(), &report), "JSON itself must have been written before the newline failed")
	assert.Equal(t, results, report.Results)
	plan := &pack.PrewarmPlan{Entries: []pack.PrewarmPlanEntry{{Level: "v1", CanPrewarm: true}}}
	newlineWriter = &prewarmNewlineErrorWriter{}
	require.ErrorIs(t, printPrewarmPlan(newlineWriter, idx, plan, true), io.ErrClosedPipe)
	var planReport map[string]any
	require.NoError(t, json.Unmarshal(newlineWriter.Bytes(), &planReport), "plan encoding must finish before the terminator error")
	assert.Equal(t, "prewarm_plan", planReport["event"])
}

func TestCLIPrewarmPropagatesJSONReportFailure(t *testing.T) {
	t.Parallel()
	fixture := makePrewarmReportingFixture(t, nil)
	dir := filepath.Join(t.TempDir(), "cache")
	// Materialization succeeds even when the command cannot report it. A later verify
	// must validate those same files and return its own reporting failure honestly.
	require.ErrorIs(t, runPrewarmReportingCommand(t, prewarmErrorWriter{}, fixture.source, dir, flagJSON), io.ErrClosedPipe)
	for i, entry := range fixture.index.Variants {
		cached, err := os.ReadFile(filepath.Join(dir, entry.SHA256))
		require.NoError(t, err)
		require.Equal(t, fixture.payloads[i], cached)
	}
	require.ErrorIs(t, runPrewarmReportingCommand(t, prewarmErrorWriter{}, fixture.source, dir, flagJSON, flagVerify),
		io.ErrClosedPipe)
	for i, entry := range fixture.index.Variants {
		cached, err := os.ReadFile(filepath.Join(dir, entry.SHA256))
		require.NoError(t, err)
		require.Equal(t, fixture.payloads[i], cached)
	}
}
