package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunDocGateCLI(t *testing.T) {
	t.Parallel()

	t.Run("passes_on_repository_root", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		code := run([]string{"--root", filepath.Join("..", "..", "..")}, &stdout, &stderr)
		assert.Equal(t, 0, code, stderr.String())
		assert.Contains(t, stdout.String(), "doc-gate: OK")
		assert.Empty(t, stderr.String())
	})

	t.Run("fails_on_invalid_flag_and_extra_args", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		assert.Equal(t, 1, run([]string{"--unknown"}, &stdout, &stderr))
		assert.Equal(t, 1, run([]string{"extra"}, &stdout, &stderr))
	})

	t.Run("fails_on_missing_root", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		code := run([]string{"--root", filepath.Join(t.TempDir(), "missing")}, &stdout, &stderr)
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr.String(), "doc-gate error:")
	})

	t.Run("fails_on_findings", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		for _, pair := range [][2]string{
			{"cmd/microfat/main.go", "package main\n"},
			{
				"internal/format/format.go",
				"package format\nconst (\n\tMagicString = \"\\x00\\xFA\\x7FMICRO\"\n" +
					"\tTrailerSize = 56\n\tIndexMagicV2 = \"\\x00\\xFAM2\"\n\tFormatVersion2 = 2\n)\n",
			},
			{"docs/cli-reference.md", "# CLI\n"},
			{"docs/architecture.md", "# Arch\n"},
		} {
			p := filepath.Join(root, filepath.FromSlash(pair[0]))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(pair[1]), 0o644))
		}
		var stdout, stderr bytes.Buffer
		code := run([]string{"--root", root}, &stdout, &stderr)
		assert.Equal(t, 1, code, stderr.String())
		assert.Contains(t, stdout.String(), "doc-gate: FAILED")
	})
}
