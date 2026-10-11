//go:build !minimal

package main

import (
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/require"
)

func TestSelectionFailureDiagnosticsAndExplicitPrewarm(t *testing.T) {
	// Launcher argv and stdout are process-wide; keep this matrix serial.
	dir := t.TempDir()
	stub, payload, source := filepath.Join(dir, "stub"), filepath.Join(dir, "payload"), filepath.Join(dir, "source")
	require.NoError(t, os.WriteFile(stub, []byte("launcher"), 0o700))
	require.NoError(t, os.WriteFile(payload, []byte("verified payload"), 0o700))
	host := microarch.Detect()
	level := "v1"
	if host.Arch == "arm64" {
		level = "v8.0"
	}
	_, err := pack.Pack(pack.Options{
		StubPath: stub, OutputPath: source, TargetArch: host.Arch,
		SkipELFValidation: true, Variants: map[string]string{level: payload},
	})
	require.NoError(t, err)
	t.Setenv(format.EnvDisableVariants, level)
	t.Setenv(format.EnvCacheDir, filepath.Join(dir, "cache"))
	oldArgs, oldStdout := os.Args, os.Stdout
	t.Cleanup(func() { os.Args, os.Stdout = oldArgs, oldStdout })
	for _, tc := range []struct {
		command string
		wantErr string
	}{
		{flagHelp, ""}, {flagInfo, ""}, {flagInfo + "=json", ""},
		{flagPrewarm + "=all,json", ""}, {flagPrewarm + "=" + level + ",verify,json", ""},
		{flagPrewarm + "=" + level, ""}, {flagPrewarm, "prewarm requires all or an explicit variant level"},
		{flagPrewarm + "=invalid", "not found in binary manifest"},
		{flagTrim, "selecting compatible CPU"}, {flagOptimize, "selecting compatible CPU"},
		{"ordinary", "selecting compatible CPU"}, {"", "selecting compatible CPU"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			output, err := os.CreateTemp(dir, "output-")
			require.NoError(t, err)
			os.Stdout = output
			os.Args = []string{source}
			if tc.command != "" {
				os.Args = append(os.Args, tc.command)
			}
			err = runBinary(source)
			os.Stdout = oldStdout
			require.NoError(t, output.Close())
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			data, err := os.ReadFile(output.Name())
			require.NoError(t, err)
			if tc.command == flagInfo+"=json" {
				var report format.BinaryInfo
				require.NoError(t, json.Unmarshal(data, &report))
				require.Empty(t, report.SelectedVariant)
				require.Zero(t, report.SelectedSize)
				require.Contains(t, report.SelectionError, "selecting compatible CPU")
				require.Empty(t, report.Runtime.Candidates)
			} else if tc.command == flagInfo || tc.command == flagHelp {
				require.Contains(t, string(data), "unavailable")
				require.Contains(t, string(data), "Selection Error:")
			}
		})
	}
}
