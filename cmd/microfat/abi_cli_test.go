package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failWriter struct {
	failOnWrite int
	writes      int
}

func (f *failWriter) Write(p []byte) (int, error) {
	f.writes++
	if f.failOnWrite == 0 || f.writes >= f.failOnWrite {
		return 0, errors.New("simulated writer error")
	}
	return len(p), nil
}

func TestPrintABIReport(t *testing.T) {
	t.Parallel()

	t.Run("nil or empty report", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		require.NoError(t, printABIReport(&buf, nil))
		assert.Empty(t, buf.String())

		require.NoError(t, printABIReport(&buf, &pack.ArtifactABIReport{}))
		assert.Empty(t, buf.String())
	})

	t.Run("skipped validation", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		rep := &pack.ArtifactABIReport{
			Status:   pack.ComparisonSkipped,
			Variants: []*pack.VariantABIReport{{Level: "v1"}},
		}
		require.NoError(t, printABIReport(&buf, rep))
		assert.Contains(t, buf.String(), "skipped (--skip-elf-validation)")

		// Writer error
		fw := &failWriter{failOnWrite: 1}
		require.Error(t, printABIReport(fw, rep))
	})

	t.Run("full report with all features", func(t *testing.T) {
		t.Parallel()
		rep := &pack.ArtifactABIReport{
			Status:     pack.ComparisonUnknown,
			Overridden: true,
			Variants: []*pack.VariantABIReport{
				{
					Level:          "v1",
					Linkage:        pack.LinkageDynamic,
					HasInterpreter: true,
					Interpreter:    "/lib64/ld-linux-x86-64.so.2",
					Dependencies:   []string{"libc.so.6", "libm.so.6"},
					VersionRequirements: []pack.VersionRequirement{
						{Library: "libc.so.6", Version: "GLIBC_2.2.5", Flags: 0},
						{Library: "libm.so.6", Version: "GLIBC_2.14", Flags: 1},
					},
					Completeness: pack.MetadataComplete,
				},
				{
					Level:          "v2",
					Linkage:        pack.LinkageStatic,
					HasInterpreter: false,
					Dependencies:   nil,
					Completeness:   pack.MetadataAbsent,
				},
			},
			DeploymentDisclaimer: "Target deployment baseline note",
		}

		var buf bytes.Buffer
		require.NoError(t, printABIReport(&buf, rep))
		out := buf.String()

		assert.Contains(t, out, "Declared ABI Requirements:")
		assert.Contains(t, out, "/lib64/ld-linux-x86-64.so.2")
		assert.Contains(t, out, "libc.so.6, libm.so.6")
		assert.Contains(t, out, "libc.so.6 (GLIBC_2.2.5)")
		assert.Contains(t, out, "libm.so.6 (GLIBC_2.14 [flags=0x0001])")
		assert.Contains(t, out, "--allow-mixed-abi")
		assert.Contains(t, out, "symbol version metadata is partial or unsupported")
		assert.Contains(t, out, "Target deployment baseline note")
	})

	t.Run("writer errors in full report", func(t *testing.T) {
		t.Parallel()
		rep := &pack.ArtifactABIReport{
			Status:     pack.ComparisonUnknown,
			Overridden: true,
			Variants: []*pack.VariantABIReport{
				{
					Level:          "v1",
					Linkage:        pack.LinkageDynamic,
					HasInterpreter: true,
					Interpreter:    "/lib64/ld-linux-x86-64.so.2",
					Dependencies:   []string{"libc.so.6"},
					VersionRequirements: []pack.VersionRequirement{
						{Library: "libc.so.6", Version: "GLIBC_2.2.5"},
					},
					Completeness: pack.MetadataComplete,
				},
			},
			DeploymentDisclaimer: "Disclaimer",
		}

		for failAt := 1; failAt <= 5; failAt++ {
			fw := &failWriter{failOnWrite: failAt}
			err := printABIReport(fw, rep)
			require.Error(t, err, "expected error when failing at write %d", failAt)
		}
	})
}
