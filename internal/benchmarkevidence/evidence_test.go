package benchmarkevidence

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/releaseworkflow"
	"github.com/stretchr/testify/require"
)

const fixtureSource = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const verifierSource = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const jsonReport = `{"fixture":"report"}`
const markdownReport = "# synthetic fixture\n"
const policyJSON = `{"fixture":"calibration policy"}`

type fixture struct {
	options Options
	values  map[string]string
	bundles []string
}

func newFixture(t *testing.T, count int) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{options: Options{Root: filepath.Join(root, "evidence"), Mode: Archive, WorkDir: filepath.Join(root, "work")},
		values: map[string]string{"SOURCE_SHA": fixtureSource, "GITHUB_RUN_ID": "1"}}
	require.NoError(t, os.MkdirAll(f.options.Root, 0o700))
	require.NoError(t, os.MkdirAll(f.options.WorkDir, 0o700))
	for _, arch := range []string{"amd64", "arm64"} {
		for _, workload := range []string{"mixed", "cpu", "memory"} {
			for _, iterations := range []int{4, 32} {
				if len(f.bundles) >= count {
					continue
				}
				bundle := filepath.Join(f.options.Root, fmt.Sprintf("%02d", len(f.bundles)))
				require.NoError(t, os.Mkdir(bundle, 0o700))
				for name, contents := range map[string]string{"SHA256SUMS": "synthetic checksum input",
					"report.json": jsonReport, "report.md": markdownReport} {
					require.NoError(t, os.WriteFile(filepath.Join(bundle, name), []byte(contents), 0o600))
				}
				var exp experiment
				exp.Source = fixtureSource
				exp.Config.Workload = workload
				exp.Config.Iterations = iterations
				exp.Environment.Host.Arch = arch
				exp.Runner.RunID = "1"
				exp.Runner.Attempt = "1"
				require.NoError(t, writeJSON(filepath.Join(bundle, "raw.json"), exp))
				f.bundles = append(f.bundles, bundle)
			}
		}
	}
	return f
}

func (f *fixture) env(key string) string { return f.values[key] }
func fixtureExecute(args ...string) ([]byte, error) {
	switch args[0] {
	case "report":
		if args[len(args)-1] == "json" {
			return []byte(jsonReport + "\n"), nil
		}
		return []byte(markdownReport + "\n"), nil
	case "qualify":
		return []byte(`{"publishable":true}`), nil
	case "calibrate":
		return []byte(policyJSON), nil
	default:
		return nil, nil
	}
}

func archiveContents(t *testing.T, file string) map[string][]byte {
	t.Helper()
	input, err := os.Open(file)
	require.NoError(t, err)
	defer input.Close()
	compressed, err := gzip.NewReader(input)
	require.NoError(t, err)
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	contents := make(map[string][]byte)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if header.Typeflag == tar.TypeDir {
			continue
		}
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		contents[header.Name] = data
	}
	return contents
}

func archivePath(t *testing.T, f *fixture) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(f.options.WorkDir, "*.tar.gz"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	return files[0]
}

func TestCompleteArchiveAndNoReplacement(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 12)
	f.values["GITHUB_STEP_SUMMARY"] = filepath.Join(f.options.WorkDir, "summary.md")
	require.NoError(t, Run(f.options, f.env, fixtureExecute, io.Discard))
	file := archivePath(t, f)
	contents := archiveContents(t, file)
	require.Len(t, contents, 49)
	require.Equal(t, []byte(jsonReport), contents["bundles/00/report.json"])
	var manifest Manifest
	require.NoError(t, json.Unmarshal(contents["verified-manifest.json"], &manifest))
	require.Equal(t, "hosted-comparative", manifest.EvidenceClass)
	require.Equal(t, "1", manifest.MeasurementRun)
	require.Equal(t, "1", manifest.MeasurementAttempt)
	require.Equal(t, "1", manifest.VerificationRun)
	require.Equal(t, "1", manifest.VerificationAttempt)
	require.Equal(t, fixtureSource, manifest.VerificationSource)
	require.Len(t, manifest.Bundles, 12)
	digest, err := releaseworkflow.HashFile(file)
	require.NoError(t, err)
	checksum, err := os.ReadFile(file + ".sha256")
	require.NoError(t, err)
	require.Equal(t, digest+"  "+filepath.Base(file)+"\n", string(checksum))
	summary, err := os.ReadFile(f.values["GITHUB_STEP_SUMMARY"])
	require.NoError(t, err)
	require.Contains(t, string(summary), "Verified 12 evidence bundles")
	require.Contains(t, string(summary), "dedicated-hardware qualification remains false")
	require.Error(t, Run(f.options, f.env, fixtureExecute, io.Discard))
	after, err := releaseworkflow.HashFile(file)
	require.NoError(t, err)
	require.Equal(t, digest, after)
}

func TestRecoveryKeepsMeasurementAndVerificationSeparate(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 12)
	f.values["GITHUB_RUN_ID"] = "2"
	f.values["GITHUB_SHA"] = verifierSource
	f.values["EVIDENCE_RUN_ID"] = "1"
	f.values["EVIDENCE_RUN_ATTEMPT"] = "1"
	require.NoError(t, Run(f.options, f.env, fixtureExecute, io.Discard))
	file := archivePath(t, f)
	require.Contains(t, filepath.Base(file), "-2-1.tar.gz")
	var manifest Manifest
	require.NoError(t, json.Unmarshal(archiveContents(t, file)["verified-manifest.json"], &manifest))
	require.Equal(t, "1", manifest.MeasurementRun)
	require.Equal(t, "2", manifest.VerificationRun)
	require.Equal(t, verifierSource, manifest.VerificationSource)
}

func TestInvalidEvidenceNeverArchives(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"missing shard", "replay", "extra newline", "source", "duplicate", "strict claim",
		"run", "attempt", "iterations", "raw absent", "raw malformed", "report absent", "verifier failure", "report failure",
		"qualifier failure", "invalid verdict", "failed verdict", "checksum disappears"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			count := 12
			if failure == "missing shard" {
				count = 11
			}
			f := newFixture(t, count)
			first := f.bundles[0]
			raw := filepath.Join(first, "raw.json")
			var exp experiment
			require.NoError(t, readJSON(raw, &exp))
			execute := fixtureExecute
			switch failure {
			case "replay":
				require.NoError(t, os.WriteFile(filepath.Join(first, "report.json"), []byte("changed"), 0o600))
			case "extra newline":
				require.NoError(t, os.WriteFile(filepath.Join(first, "report.json"), []byte(jsonReport+"\n"), 0o600))
			case "source":
				exp.Source = verifierSource
			case "duplicate":
				require.NoError(t, readJSON(filepath.Join(f.bundles[1], "raw.json"), &exp))
			case "strict claim":
				exp.ReleaseEligible = true
			case "run":
				exp.Runner.RunID = "2"
			case "attempt":
				exp.Runner.Attempt = "2"
			case "iterations":
				exp.Config.Iterations = 5
			case "report absent":
				require.NoError(t, os.Remove(filepath.Join(first, "report.md")))
			case "verifier failure", "report failure", "qualifier failure":
				command := map[string]string{"verifier failure": "verify", "report failure": "report", "qualifier failure": "qualify"}[failure]
				execute = func(args ...string) ([]byte, error) {
					if args[0] == command {
						return nil, errors.New("command failed")
					}
					return fixtureExecute(args...)
				}
			case "invalid verdict", "failed verdict":
				execute = func(args ...string) ([]byte, error) {
					if args[0] == "qualify" {
						if failure == "invalid verdict" {
							return []byte("{"), nil
						}
						return []byte(`{"publishable":false}`), nil
					}
					return fixtureExecute(args...)
				}
			case "checksum disappears":
				execute = func(args ...string) ([]byte, error) {
					if args[0] == "qualify" {
						require.NoError(t, os.Remove(filepath.Join(first, "SHA256SUMS")))
					}
					return fixtureExecute(args...)
				}
			}
			require.NoError(t, writeJSON(raw, exp))
			if failure == "raw absent" {
				require.NoError(t, os.Remove(raw))
			}
			if failure == "raw malformed" {
				require.NoError(t, os.WriteFile(raw, []byte("{"), 0o600))
			}
			require.Error(t, Run(f.options, f.env, execute, io.Discard))
			files, err := filepath.Glob(filepath.Join(f.options.WorkDir, "*.tar.gz"))
			require.NoError(t, err)
			require.Empty(t, files)
		})
	}
}

func TestCalibrationAndShard(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 1)
	f.options.Mode = Calibration
	f.values["GITHUB_STEP_SUMMARY"] = filepath.Join(f.options.WorkDir, "summary.md")
	var out bytes.Buffer
	require.NoError(t, Run(f.options, f.env, fixtureExecute, &out))
	require.Equal(t, policyJSON+"\n", out.String())
	var paths []string
	require.NoError(t, readJSON(filepath.Join(f.options.WorkDir, "calibration-bundles.json"), &paths))
	require.Equal(t, f.bundles, paths)
	policy, err := os.ReadFile(filepath.Join(f.options.WorkDir, "calibration-policy.json"))
	require.NoError(t, err)
	require.Equal(t, out.Bytes(), policy)
	summary, err := os.ReadFile(f.values["GITHUB_STEP_SUMMARY"])
	require.NoError(t, err)
	require.NotContains(t, string(summary), "qualification")
	f.options.Mode = Shard
	require.NoError(t, Run(f.options, f.env, fixtureExecute, io.Discard))
	delete(f.values, "SOURCE_SHA")
	delete(f.values, "GITHUB_RUN_ID")
	require.NoError(t, Run(f.options, f.env, fixtureExecute, io.Discard))
	files, err := filepath.Glob(filepath.Join(f.options.WorkDir, "*.tar.gz"))
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestInputAndOutputFailures(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 0)
	require.Error(t, Run(f.options, f.env, fixtureExecute, io.Discard))
	f.options.Mode = "invalid"
	require.Error(t, Run(f.options, f.env, fixtureExecute, io.Discard))
	_, err := discover(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
	f = newFixture(t, 1)
	f.options.Mode = Calibration
	require.Error(t, Run(f.options, f.env, func(args ...string) ([]byte, error) {
		if args[0] == "calibrate" {
			return nil, errors.New("calibration failed")
		}
		return fixtureExecute(args...)
	}, io.Discard))
	require.Error(t, Run(f.options, f.env, fixtureExecute, failingWriter{}))
	require.Error(t, writeJSON(filepath.Join(t.TempDir(), "bad"), make(chan int)))
	for _, name := range []string{"calibration-bundles.json", "calibration-policy.json"} {
		f := newFixture(t, 1)
		f.options.Mode = Calibration
		require.NoError(t, os.Mkdir(filepath.Join(f.options.WorkDir, name), 0o700))
		require.Error(t, Run(f.options, f.env, fixtureExecute, io.Discard))
	}
	require.Error(t, appendSummary(t.TempDir(), 1, Shard))
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestArchiveConfinementAndPartialCleanup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.WriteFile(outside, []byte("private"), 0o600))
	bundle := filepath.Join(root, "bundle")
	require.NoError(t, os.Mkdir(bundle, 0o700))
	require.NoError(t, os.Symlink(outside, filepath.Join(bundle, "link")))
	manifest := filepath.Join(root, "manifest")
	require.NoError(t, os.WriteFile(manifest, []byte("{}"), 0o600))
	file := filepath.Join(root, "archive.tar.gz")
	require.Error(t, writeArchive(file, manifest, []VerifiedBundle{{Bundle: bundle}}))
	require.NoFileExists(t, file)
	require.Error(t, writeArchive(file, filepath.Join(root, "missing"), nil))
	require.NoFileExists(t, file)
	require.Error(t, addTree(tar.NewWriter(failingWriter{}), manifest, "manifest"))
	scope, err := os.OpenRoot(root)
	require.NoError(t, err)
	require.Error(t, writeTree(tar.NewWriter(io.Discard), scope, "missing", "missing"))
	require.NoError(t, scope.Close())
	require.Error(t, writeTree(tar.NewWriter(io.Discard), scope, "bundle", "bundle"))
}

func TestArchiveMetadataFailures(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"SOURCE_SHA", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT"} {
		f := newFixture(t, 12)
		f.values[key] = "bad"
		require.Error(t, Run(f.options, f.env, fixtureExecute, io.Discard))
	}
	f := newFixture(t, 12)
	require.NoError(t, os.Mkdir(filepath.Join(f.options.Root, "verified-manifest.json"), 0o700))
	require.Error(t, Run(f.options, f.env, fixtureExecute, io.Discard))
	f = newFixture(t, 12)
	f.options.WorkDir = filepath.Join(f.options.WorkDir, "missing")
	require.Error(t, Run(f.options, f.env, fixtureExecute, io.Discard))
	f = newFixture(t, 12)
	name := filepath.Join(f.options.WorkDir, "benchmark-evidence-"+fixtureSource+"-1-1.tar.gz.sha256")
	require.NoError(t, os.Mkdir(name, 0o700))
	require.Error(t, Run(f.options, f.env, fixtureExecute, io.Discard))
}

func TestRelativeCalibrationNeedsWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Remove(root))
	require.Error(t, calibrate("work", []string{"relative"}, nil, io.Discard))
}

func TestSourceCohort(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, err := SourceCohort(root, "1", "2")
	require.Error(t, err)
	require.NoError(t, writeJSON(filepath.Join(root, "source-run.json"), releaseworkflow.WorkflowRun{
		ID: 1, Attempt: 2, HeadSHA: fixtureSource, Status: "completed", Path: releaseworkflow.BenchmarkWorkflow}))
	_, err = SourceCohort(root, "1", "2")
	require.Error(t, err)
	var jobs []releaseworkflow.Job
	for _, name := range releaseworkflow.MeasurementJobs() {
		jobs = append(jobs, releaseworkflow.Job{Name: name, Conclusion: "success"})
	}
	require.NoError(t, writeJSON(filepath.Join(root, "source-jobs.json"), map[string]any{"jobs": jobs}))
	source, err := SourceCohort(root, "1", "2")
	require.NoError(t, err)
	require.Equal(t, fixtureSource, source)
	require.NotEmpty(t, strings.TrimSpace(source))
}
