package report

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/compare"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/internal/testfixture"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportReplay(t *testing.T) {
	t.Parallel()
	exp := testfixture.Experiment()
	comparisons, err := compare.All(exp, "base")
	require.NoError(t, err)
	exp.Comparisons = comparisons
	exp.Warnings = []string{"synthetic <fixture> | never a performance claim"}
	exp.Trials[0].Metrics["unavailable"] = schema.Unavailable("bytes", "startup", "fixture", "no probe")
	exp.Trials[1].Metrics = nil
	exp.Comparisons = nil
	exp.Comparisons, err = compare.All(exp, "base")
	require.NoError(t, err)
	for _, format := range []string{"terminal", "markdown", "json"} {
		t.Run(format, func(t *testing.T) {
			a, err := Render(exp, format)
			require.NoError(t, err)
			b, err := Render(exp, format)
			require.NoError(t, err)
			assert.Equal(t, a, b)
			assert.Contains(t, string(a), "fixture")
		})
	}
	_, err = Render(exp, "unsupported")
	require.Error(t, err)
	_, err = Render(nil, "terminal")
	require.Error(t, err)
	destination := filepath.Join(t.TempDir(), "bundle")
	require.NoError(t, WriteBundle(exp, destination, nil))
	read, err := ReadBundle(destination)
	require.NoError(t, err)
	assert.Equal(t, exp.ID, read.ID)
	require.Error(t, WriteBundle(exp, destination, nil))
	files, err := VerifyFiles(destination)
	require.NoError(t, err)
	assert.Contains(t, files, "SHA256SUMS")
	require.NoError(t, os.WriteFile(filepath.Join(destination, "raw.json"), []byte("corrupted"), fileMode))
	_, err = ReadBundle(destination)
	require.ErrorContains(t, err, "digest mismatch")
}

func TestBundleRejectsUnsafeOrIncompleteInputs(t *testing.T) {
	t.Parallel()
	exp := testfixture.Experiment()
	for name, extra := range map[string]map[string][]byte{
		"traversal": {"../x": []byte("x")}, "reserved": {"raw.json": []byte("x")},
	} {
		t.Run(name, func(t *testing.T) { require.Error(t, WriteBundle(exp, filepath.Join(t.TempDir(), "bundle"), extra)) })
	}
	require.Error(t, WriteBundle(nil, filepath.Join(t.TempDir(), "bundle"), nil))
	http := testfixture.HTTP()
	exp.Trials[0].Load = &http
	require.ErrorContains(t, WriteBundle(exp, filepath.Join(t.TempDir(), "bundle"), nil), "missing raw load evidence")
	dest := filepath.Join(t.TempDir(), "bundle")
	require.NoError(t, WriteBundle(exp, dest, map[string][]byte{http.RawPath: []byte("{}")}))
	require.NoError(t, os.Symlink("raw.json", filepath.Join(dest, "link")))
	_, err := VerifyFiles(dest)
	require.ErrorContains(t, err, "non-regular")
	_, err = VerifyFiles(filepath.Join(dest, "link"))
	require.Error(t, err)
	_, err = ReadBounded(filepath.Join(dest, "link"))
	require.Error(t, err)
	_, err = ReadBounded("/missing")
	require.Error(t, err)
	_, err = ReadBounded(dest)
	require.Error(t, err)
	_, err = VerifyFiles("/missing")
	require.Error(t, err)
}

func TestChecksumManifestValidation(t *testing.T) {
	t.Parallel()
	for name, manifest := range map[string]string{
		"missing": "", "invalid": "wrong", "traversal": strings.Repeat("a", 64) + "  ../x\n",
		"self":         strings.Repeat("a", 64) + "  SHA256SUMS\n",
		"missing file": schema.Digest([]byte("x")) + "  missing\n",
		"duplicate":    strings.Repeat(schema.Digest([]byte("x"))+"  x\n", 2),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "x"), []byte("x"), fileMode))
			require.NoError(t, os.WriteFile(filepath.Join(root, checksumName), []byte(manifest), fileMode))
			_, err := VerifyFiles(root)
			require.Error(t, err)
		})
	}
	root := t.TempDir()
	require.NoError(t, writeFiles(root, map[string][]byte{"x": []byte("x")}))
	require.NoError(t, os.WriteFile(filepath.Join(root, "unlisted"), nil, fileMode))
	_, err := VerifyFiles(root)
	require.ErrorContains(t, err, "unlisted")
	require.Error(t, writeFiles(t.TempDir(), map[string][]byte{"../escape": nil}))
}

func TestReportFilesystemFailures(t *testing.T) {
	t.Parallel()
	exp := testfixture.Experiment()
	root := t.TempDir()
	file := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(file, []byte("x"), fileMode))
	require.Error(t, WriteBundle(exp, filepath.Join(file, "bundle"), nil))
	require.Error(t, writeFiles(file, map[string][]byte{"nested/file": nil}))
	require.Error(t, writeFiles(root, map[string][]byte{"file/child": nil}))
	require.NoError(t, os.Mkdir(filepath.Join(root, "directory"), dirMode))
	require.Error(t, writeFiles(root, map[string][]byte{"directory": nil}))
	require.NoError(t, os.Mkdir(filepath.Join(root, checksumName), dirMode))
	require.Error(t, writeFiles(root, map[string][]byte{"valid": nil}))
	_, err := openEvidence(filepath.Join(root, "absent"))
	require.Error(t, err)
	for _, raw := range []string{"{", `{"schema_version":"future"}`} {
		bundle := t.TempDir()
		require.NoError(t, writeFiles(bundle, map[string][]byte{"raw.json": []byte(raw)}))
		_, err := ReadBundle(bundle)
		require.Error(t, err)
		_, err = RenderInput(bundle, "json")
		require.Error(t, err)
	}
	valid := filepath.Join(t.TempDir(), "bundle")
	require.NoError(t, WriteBundle(exp, valid, nil))
	_, err = VerifyInput(valid)
	require.NoError(t, err)
	_, err = RenderInput(valid, "json")
	require.NoError(t, err)
	_, _, err = ReadLegacy("/missing")
	require.Error(t, err)
}

func TestEvidenceResourceBounds(t *testing.T) {
	t.Parallel()
	oversized := filepath.Join(t.TempDir(), "oversized")
	f, err := os.Create(oversized)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(schema.MaxEvidenceBytes+1))
	require.NoError(t, f.Close())
	_, err = ReadBounded(oversized)
	require.Error(t, err)
	root := t.TempDir()
	require.NoError(t, os.Link(oversized, filepath.Join(root, "oversized")))
	_, err = VerifyFiles(root)
	require.Error(t, err)
	data := make([]byte, schema.MaxEvidenceBytes+1)
	require.ErrorContains(t, writeFiles(t.TempDir(), map[string][]byte{"large": data}), "byte limit")
	files := make(map[string][]byte, maxBundleFiles+1)
	for i := 0; i <= maxBundleFiles; i++ {
		files[strconv.Itoa(i)] = nil
	}
	require.ErrorContains(t, writeFiles(t.TempDir(), files), "too many")
	// Reuse one backing allocation to check aggregate bounds without allocating 512 MiB.
	files = make(map[string][]byte)
	for i := 0; i < 10; i++ {
		files[strconv.Itoa(i)] = data[:schema.MaxEvidenceBytes]
	}
	require.ErrorContains(t, writeFiles(t.TempDir(), files), "byte limit")
}

func TestDescriptorValidationAndCompleteIntervals(t *testing.T) {
	t.Parallel()
	file, err := os.CreateTemp(t.TempDir(), "closed")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	_, err = readDescriptor(file)
	require.Error(t, err)
	exp := testfixture.Experiment()
	exp.Comparisons, err = compare.All(exp, "base")
	require.NoError(t, err)
	rendered, err := Render(exp, "terminal")
	require.NoError(t, err)
	assert.Contains(t, string(rendered), "95% CI [0, 0]")
}

func TestExternalTelemetryVerification(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"missing", "[", `[]`, `[{"elapsed_ns":-1}]`, `[{"elapsed_ns":1,"phase":"startup"}]`} {
		t.Run(raw, func(t *testing.T) {
			exp := testfixture.Experiment()
			exp.Trials[0].TelemetryPath = "trials/trial-0/telemetry.json"
			exp.Trials[0].SampleCount, exp.Trials[0].SampleIntervalMS = 1, 25
			files := map[string][]byte{}
			if raw != "missing" {
				files[exp.Trials[0].TelemetryPath] = []byte(raw)
			}
			err := WriteBundle(exp, filepath.Join(t.TempDir(), "bundle"), files)
			if raw == `[{"elapsed_ns":1,"phase":"startup"}]` {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
