package runtimequalify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/releaseaudit"
	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
)

// These are orchestration unit fakes, never accepted as native evidence.
type runtimeFake struct {
	t      *testing.T
	c      *controller
	calls  []process.Spec
	failed string
	next   int
}

func flagValue(args []string, name string) string {
	i := slices.Index(args, name)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

func fakeController(t *testing.T, arch string) (*controller, *runtimeFake) {
	t.Helper()
	c := &controller{options: Options{Input: Source, Tests: Required, Backend: "userns", Output: t.TempDir(), Go: "go"},
		summary: Summary{Schema: Schema, Input: Source, Architecture: arch, Products: map[string]string{}, Expected: Cases()[:6]},
		bundles: map[string]Lineage{}}
	f := &runtimeFake{t: t, c: c}
	c.runner = releaseaudit.Runner{Context: context.Background(), Output: c.options.Output, Execute: f.execute}
	return c, f
}

func (f *runtimeFake) execute(_ context.Context, spec process.Spec) (releaseaudit.Result, error) {
	f.calls = append(f.calls, spec)
	if f.failed != "" && (spec.Path == f.failed || (len(spec.Args) > 0 && spec.Args[0] == f.failed)) {
		return releaseaudit.Result{}, errors.New("injected command failure")
	}
	result := releaseaudit.Result{}
	switch spec.Path {
	case "go":
		if spec.Args[0] == "version" {
			result.Stdout = "go version go1.27.1 linux/" + f.c.summary.Architecture
			break
		}
		require.Equal(f.t, "build", spec.Args[0])
		require.NoError(f.t, os.WriteFile(flagValue(spec.Args, "-o"), []byte("fake "+spec.Args[len(spec.Args)-1]), privateMode))
	case "git":
		switch spec.Args[0] {
		case "rev-parse":
			result.Stdout = testSource
		case "status":
		case "diff":
			result.Stdout = "retained patch"
		case "ls-files":
			result.Stdout = "scenarios.go\x00"
		default:
			f.t.Fatalf("unexpected git call: %v", spec)
		}
	case "uname":
		result.Stdout = "Linux " + map[string]string{amd64: "x86_64", arm64: "aarch64"}[f.c.summary.Architecture]
	case "unshare", Sudo:
		if spec.Args[len(spec.Args)-1] != "true" {
			result.Stdout = f.observation(spec)
		}
	default:
		require.Equal(f.t, "microfat", filepath.Base(spec.Path))
		switch spec.Args[0] {
		case "detect":
			result.Stdout = fmt.Sprintf(`{"Arch":%q,"Level":%q}`, f.c.summary.Architecture,
				map[string]string{amd64: "v3", arm64: "v9.0"}[f.c.summary.Architecture])
		case "pack":
			fakeBundle(f.t, spec.Args)
		case "verify":
		default:
			f.t.Fatalf("unexpected CLI call: %v", spec)
		}
	}
	return result, nil
}

func fakeBundle(t *testing.T, args []string) {
	t.Helper()
	stub, err := os.ReadFile(flagValue(args, "--stub"))
	require.NoError(t, err)
	reporter := strings.SplitN(flagValue(args, "-v"), "=", 2)[1]
	payload, err := os.ReadFile(reporter)
	require.NoError(t, err)
	var data bytes.Buffer
	_, err = data.Write(stub)
	require.NoError(t, err)
	index := format.Index{TargetOS: "linux", TargetArch: flagValue(args, "--arch")}
	if slices.Contains(args, "--dict") {
		index.DictionaryOffset, index.DictionarySize, index.DictionaryID = int64(data.Len()), 4, 1
		_, err = data.WriteString("dict")
		require.NoError(t, err)
		index.DictionarySHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte("dict")))
	}
	index.Variants = []format.VariantEntry{{Level: strings.SplitN(flagValue(args, "-v"), "=", 2)[0], Offset: int64(data.Len()),
		CompressedSize: int64(len(payload)), UncompressedSize: int64(len(payload)),
		SHA256: fmt.Sprintf("%x", sha256.Sum256(payload)), Compression: "none"}}
	_, err = data.Write(payload)
	require.NoError(t, err)
	version, err := strconv.Atoi(flagValue(args, "--format-version"))
	require.NoError(t, err)
	_, err = format.WriteIndexAndTrailerWithVersion(&data, &index, int64(data.Len()), version)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(flagValue(args, "-o"), data.Bytes(), privateMode))
}

func (f *runtimeFake) observation(spec process.Spec) string {
	t := f.t
	data, err := os.ReadFile(spec.Args[len(spec.Args)-1])
	require.NoError(t, err)
	var request mountfixture.Request
	require.NoError(t, json.Unmarshal(data, &request))
	item := f.c.summary.Expected[f.next]
	f.next++
	e := validEvidence(t, item)
	hash := f.c.summary.Products["reporter"]
	info, err := os.Stat(f.c.reporter)
	require.NoError(t, err)
	for i := range e.Result.Executions {
		run := &e.Result.Executions[i]
		if run.Stdout != "" && item.Scenario != policyControl && item.Scenario != distributedCLI {
			var report mountfixture.Report
			require.NoError(t, json.Unmarshal([]byte(run.Stdout), &report))
			report.UID, report.EUID, report.GID, report.EGID = request.UID, request.UID, request.GID, request.GID
			report.Args = append([]string{request.Command}, request.Args...)
			report.Stdin = request.Stdin
			report.Digest = hash
			run.Stdout = jsonString(t, report)
		}
		run.Stderr = strings.ReplaceAll(run.Stderr, testHash, hash)
		run.Stderr = strings.ReplaceAll(run.Stderr, `"selected_size_bytes":32`, fmt.Sprintf(`"selected_size_bytes":%d`, info.Size()))
		tier := f.c.bundles[item.Configuration.Name()].SelectedTier
		run.Stderr = strings.ReplaceAll(run.Stderr, `"selected_variant":"v1"`, `"selected_variant":"`+tier+`"`)
		for j := range run.PolicyEvents {
			if run.PolicyEvents[j].Digest != "" {
				run.PolicyEvents[j].Digest = hash
			}
		}
	}
	for i := range e.Result.CacheSnapshots {
		for j := range e.Result.CacheSnapshots[i].Entries {
			entry := &e.Result.CacheSnapshots[i].Entries[j]
			entry.Name, entry.Digest, entry.Size, entry.UID = hash, hash, info.Size(), uint32(request.UID)
		}
	}
	return jsonString(t, e.Result)
}

func TestSourceControllerAndAllFixtures(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{amd64, arm64} {
		t.Run(arch, func(t *testing.T) {
			c, f := fakeController(t, arch)
			// Exercise every scenario and both phases, with one instance of the matrix;
			// packing below still covers all 16 configurations.
			c.summary.Expected = Cases()[:70]
			require.NoError(t, c.qualify(io.Discard))
			require.Equal(t, Pass, c.summary.Status)
			require.Len(t, c.bundles, 16)
			require.Len(t, c.summary.Results, len(c.summary.Expected))
			for _, e := range c.summary.Results {
				require.Equal(t, Pass, e.Status, e.Reason)
				_, err := os.Stat(e.Request.Root)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			f.next = 0
			c.summary.Expected = []Case{Cases()[len(Cases())-1]}
			_, err := c.exercise(c.summary.Expected[0])
			require.NoError(t, err)
			require.NoError(t, c.retainSource())
			_, err = os.Stat(filepath.Join(c.options.Output, "untracked-source", "scenarios.go"))
			require.NoError(t, err)
		})
	}
}

func TestRunRetainsExpectedManifestOnEarlyFailure(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"version", "rev-parse", "status", "uname", "unshare", "build", "pack", "verify"} {
		t.Run(stage, func(t *testing.T) {
			c, f := fakeController(t, runtime.GOARCH)
			f.failed = stage
			err := Run(context.Background(), c.options, io.Discard, []string{"GITHUB_RUN_ID=4", "GITHUB_RUN_ATTEMPT=2"}, f.execute)
			require.Error(t, err)
			files, err := filepath.Glob(filepath.Join(c.options.Output, "run-*", "summary.json"))
			require.NoError(t, err)
			require.Len(t, files, 1)
			var summary Summary
			data, err := os.ReadFile(files[0])
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &summary))
			require.Equal(t, Cases(), summary.Expected)
			require.Equal(t, Fail, summary.Status)
			require.NotEmpty(t, summary.Error)
			require.Equal(t, "4", summary.RunID)
		})
	}
}

func TestAutoOnlyToleratesKnownMissingPrerequisites(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		message string
		missing bool
	}{
		{"unshare: unshare failed: Operation not permitted", true},
		{"sudo: a password is required", true},
		{"unexpected setup crash", false},
	} {
		t.Run(tc.message, func(t *testing.T) {
			c, _ := fakeController(t, runtime.GOARCH)
			c.runner.Execute = func(context.Context, process.Spec) (releaseaudit.Result, error) {
				return releaseaudit.Result{ExitCode: 1, Stderr: tc.message}, nil
			}
			missing, err := c.preflight()
			require.Error(t, err)
			require.Equal(t, tc.missing, missing)
		})
	}
}

func TestRequestAndAcquisitionFailures(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"products-directory", "reporter", "helper", "bundle",
		"invalid-bundle", "source-revision", "dirty-candidate"} {
		t.Run(failure, func(t *testing.T) {
			c, _ := fakeController(t, runtime.GOARCH)
			switch failure {
			case "products-directory":
				require.NoError(t, os.Mkdir(filepath.Join(c.options.Output, "products"), privateMode))
				require.Error(t, c.acquire())
			case "source-revision", "dirty-candidate":
				c.options.Input, c.options.Source = Candidate, testSource
				c.summary.Dirty = failure == "dirty-candidate"
				require.Error(t, c.acquire())
			default:
				require.NoError(t, c.acquire())
				require.NoError(t, c.buildHelpers())
				l, err := c.pack(Configurations()[0])
				require.NoError(t, err)
				switch failure {
				case "reporter":
					require.NoError(t, os.Remove(c.reporter))
					_, err = c.pack(Configurations()[0])
				case "helper":
					require.NoError(t, os.Remove(c.helper))
					_, err = c.request(Cases()[4], l)
				case "bundle":
					require.NoError(t, os.Remove(l.Bundle))
					_, err = c.request(Cases()[4], l)
				case "invalid-bundle":
					require.NoError(t, os.WriteFile(l.Bundle, []byte("broken"), privateMode))
					err = corruptBundle(l.Bundle, false)
				}
				require.Error(t, err)
			}
		})
	}
}

func TestFilesystemAndOptionBoundaries(t *testing.T) {
	t.Parallel()
	options := Options{Input: Source, Tests: Required, Backend: "userns", Output: t.TempDir(), Go: "go"}
	require.NoError(t, options.Validate("linux", amd64))
	require.NoError(t, options.Validate("linux", arm64))
	require.Error(t, options.Validate("darwin", amd64))
	require.Error(t, options.Validate("linux", "386"))
	options.Tag = "v0.3.0"
	require.Error(t, options.Validate("linux", amd64))
	options.Input, options.Dist, options.Source = Candidate, t.TempDir(), testSource
	require.NoError(t, options.Validate("linux", amd64))
	options.Dist = "relative"
	require.Error(t, options.Validate("linux", amd64))
	require.Error(t, WriteJSON(filepath.Join(t.TempDir(), "file"), make(chan bool)))
	require.Error(t, copyFile("/absent", filepath.Join(t.TempDir(), "out"), dataMode, false))
	path := filepath.Join(t.TempDir(), "source")
	require.NoError(t, os.WriteFile(path, []byte("content"), dataMode))
	require.Error(t, copyFile(path, path, dataMode, false))
	target := filepath.Join(t.TempDir(), "target")
	require.NoError(t, copyFile(path, target, dataMode, true))
	require.Equal(t, "x", envValue([]string{"KEY=x"}, "KEY"))
	require.Empty(t, envValue(nil, "KEY"))
	require.Equal(t, "sudo", namespaceCommand(Sudo, "true")[0])
}

func TestEvidenceWritesRemainComplete(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "summary.json")
	value := strings.Repeat("evidence", 1<<13)
	require.NoError(t, WriteJSON(path, value))
	done := make(chan error, 1)
	defer func() { <-done }()
	go func() {
		defer close(done)
		for range 32 {
			if err := WriteJSON(path, value); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var actual string
		require.NoError(t, json.Unmarshal(data, &actual), "a concurrent reader must never observe a partial summary")
		require.Equal(t, value, actual)
		select {
		case err := <-done:
			require.NoError(t, err)
			entries, err := os.ReadDir(root)
			require.NoError(t, err)
			require.Len(t, entries, 1, "successful writes must clean temporary files")
			return
		default:
		}
	}
}

func TestEvidenceWriteFailurePreservesExistingRecord(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "summary.json")
	require.NoError(t, WriteJSON(path, "previous"))
	require.Error(t, WriteJSON(path, make(chan bool)))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.JSONEq(t, `"previous"`, string(data))
	require.Error(t, WriteJSON(filepath.Join(path, "missing-parent"), "next"))
	require.Error(t, WriteJSON(root, "cannot replace a directory"))
	entries, err := filepath.Glob(filepath.Join(filepath.Dir(root), ".runtime-evidence-*"))
	require.NoError(t, err)
	require.Empty(t, entries, "failed rename must clean temporary files")
}
