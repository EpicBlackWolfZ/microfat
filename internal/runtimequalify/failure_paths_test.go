package runtimequalify

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/releaseaudit"
	"github.com/stretchr/testify/require"
)

type brokenProgress struct{}

func (brokenProgress) Write([]byte) (int, error) { return 0, errors.New("progress unavailable") }

func TestAcquisitionCannotAcceptMissingBuildOutputs(t *testing.T) {
	t.Parallel()
	for _, helper := range []bool{false, true} {
		for _, failure := range []string{"directory", "build", "missing-output", "settings"} {
			t.Run(strings.Join([]string{map[bool]string{true: "helper", false: "product"}[helper], failure}, "/"), func(t *testing.T) {
				c, f := fakeController(t, runtime.GOARCH)
				operation, directory := c.acquire, "products"
				if helper {
					operation, directory = c.buildHelpers, "helpers"
				}
				if failure == "directory" {
					require.NoError(t, os.WriteFile(filepath.Join(c.options.Output, directory), nil, dataMode))
				}
				c.runner.Execute = func(ctx context.Context, spec process.Spec) (releaseaudit.Result, error) {
					if failure == "build" || (failure == "settings" && spec.Args[0] == "version") {
						return releaseaudit.Result{}, errors.New("tool failed")
					}
					if failure == "missing-output" {
						return releaseaudit.Result{}, nil
					}
					return f.execute(ctx, spec)
				}
				require.Error(t, operation())
			})
		}
	}
}

func TestPackingRejectsSubstitutedAndMalformedProducts(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"candidate", "candidate-tag", "detect", "detect-json", "detect-arch",
		"missing-stub", "missing-bundle", "short-bundle", "substituted-stub", "verification"} {
		t.Run(failure, func(t *testing.T) {
			c, f := fakeController(t, runtime.GOARCH)
			require.NoError(t, c.acquire())
			require.NoError(t, c.buildHelpers())
			configuration := Configurations()[3]
			if strings.HasPrefix(failure, "candidate") {
				c.options.Input, c.options.Tag = Candidate, testCandidateTag
				if failure == "candidate-tag" {
					c.options.Tag = ""
				}
			}
			c.runner.Execute = func(ctx context.Context, spec process.Spec) (releaseaudit.Result, error) {
				if spec.Args[0] == "detect" {
					switch failure {
					case "detect":
						return releaseaudit.Result{}, errors.New("detection failed")
					case "detect-json":
						return releaseaudit.Result{Stdout: "{"}, nil
					case "detect-arch":
						return releaseaudit.Result{Stdout: `{"Arch":"unsupported"}`}, nil
					}
				}
				if spec.Args[0] == "verify" && failure == "verification" {
					return releaseaudit.Result{}, errors.New("verification failed")
				}
				result, err := f.execute(ctx, spec)
				if spec.Args[0] == "pack" {
					bundle := flagValue(spec.Args, "-o")
					switch failure {
					case "missing-stub":
						require.NoError(t, os.Remove(flagValue(spec.Args, "--stub")))
					case "missing-bundle":
						require.NoError(t, os.Remove(bundle))
					case "short-bundle":
						require.NoError(t, os.WriteFile(bundle, nil, privateMode))
					case "substituted-stub":
						require.NoError(t, os.WriteFile(bundle, []byte(strings.Repeat(testWrongValue, 100)), privateMode))
					}
				}
				return result, err
			}
			lineage, err := c.pack(configuration)
			if failure == "candidate" {
				require.NoError(t, err)
				require.Contains(t, lineage.Archive, "0.3.0_linux_"+runtime.GOARCH)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestSourceRetentionRefusesUnreproducibleSnapshots(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{testDiff, "patch", "list", "unsafe", "directory", "missing-source"} {
		t.Run(failure, func(t *testing.T) {
			c, f := fakeController(t, runtime.GOARCH)
			if failure == "patch" {
				require.NoError(t, os.Mkdir(filepath.Join(c.options.Output, "source.patch"), privateMode))
			}
			if failure == "directory" {
				require.NoError(t, os.WriteFile(filepath.Join(c.options.Output, "untracked-source"), nil, dataMode))
			}
			c.runner.Execute = func(ctx context.Context, spec process.Spec) (releaseaudit.Result, error) {
				if (failure == testDiff && spec.Args[0] == testDiff) || (failure == "list" && spec.Args[0] == "ls-files") {
					return releaseaudit.Result{}, errors.New("source unavailable")
				}
				if spec.Args[0] == "ls-files" {
					if failure == "unsafe" {
						return releaseaudit.Result{Stdout: "../escape\x00"}, nil
					}
					if failure == "missing-source" {
						return releaseaudit.Result{Stdout: "missing-source-file\x00"}, nil
					}
				}
				return f.execute(ctx, spec)
			}
			require.Error(t, c.retainSource())
		})
	}
}

func TestQualificationPrerequisitesAndPersistenceFailures(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"toolchain", "kernel", "summary", "dirty", "dirty-snapshot", "helpers", Auto, "required"} {
		t.Run(failure, func(t *testing.T) {
			c, f := fakeController(t, runtime.GOARCH)
			if failure == "summary" {
				require.NoError(t, os.Mkdir(filepath.Join(c.options.Output, "summary.json"), privateMode))
			}
			if failure == Auto {
				c.options.Tests = Auto
				c.summary.Status = Incomplete
			}
			c.runner.Execute = func(ctx context.Context, spec process.Spec) (releaseaudit.Result, error) {
				switch {
				case failure == "toolchain" && spec.Path == "go":
					return releaseaudit.Result{Stdout: "go version go1.26 linux/amd64"}, nil
				case failure == "kernel" && spec.Path == "uname":
					return releaseaudit.Result{Stdout: "emulated foreign kernel"}, nil
				case strings.HasPrefix(failure, "dirty") && spec.Path == "git" && spec.Args[0] == testStatus:
					return releaseaudit.Result{Stdout: " M source.go"}, nil
				case failure == "dirty-snapshot" && spec.Path == "git" && spec.Args[0] == testDiff:
					return releaseaudit.Result{}, errors.New("cannot retain source")
				case failure == "helpers" && strings.Contains(spec.Args[len(spec.Args)-1], "mount_runner"):
					return releaseaudit.Result{}, errors.New("cannot build helper")
				case (failure == Auto || failure == "required") && spec.Path == "unshare":
					return releaseaudit.Result{}, exec.ErrNotFound
				}
				return f.execute(ctx, spec)
			}
			err := c.qualify(io.Discard)
			if failure == "dirty" || failure == Auto {
				require.NoError(t, err)
				if failure == Auto {
					require.Equal(t, Incomplete, c.summary.Status)
					require.NotEmpty(t, c.summary.Error)
				}
			} else {
				require.Error(t, err)
			}
		})
	}
}

type progressFunc func([]byte) (int, error)

func (f progressFunc) Write(data []byte) (int, error) { return f(data) }

func TestManifestMustBeWritableBeforeAnyAcquisition(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"options", "parent-file", "parent-readonly", "progress", "manifest"} {
		t.Run(failure, func(t *testing.T) {
			if os.Getuid() == 0 {
				t.Skip("permission errors require ordinary credentials")
			}
			c, _ := fakeController(t, runtime.GOARCH)
			var out io.Writer = io.Discard
			switch failure {
			case "options":
				c.options.Backend = "host"
			case "parent-file":
				c.options.Output = filepath.Join(c.options.Output, "file")
				require.NoError(t, os.WriteFile(c.options.Output, nil, dataMode))
			case "parent-readonly":
				require.NoError(t, os.Chmod(c.options.Output, 0o500))
				t.Cleanup(func() { require.NoError(t, os.Chmod(c.options.Output, privateMode)) })
			case "progress":
				out = brokenProgress{}
			case "manifest":
				out = progressFunc(func(data []byte) (int, error) {
					fields := strings.Fields(string(data))
					path := fields[len(fields)-1]
					require.NoError(t, os.Chmod(path, 0o500))
					t.Cleanup(func() { require.NoError(t, os.Chmod(path, privateMode)) })
					return len(data), nil
				})
			}
			require.Error(t, Run(context.Background(), c.options, out, nil,
				func(context.Context, process.Spec) (releaseaudit.Result, error) {
					t.Fatal("acquisition must not start without a retained manifest")
					return releaseaudit.Result{}, nil
				}))
		})
	}
}

func TestDeletedWorkingDirectoryFailsBeforeExecution(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Remove(root))
	options := Options{Input: Source, Tests: Auto, Backend: "userns", Output: "relative", Go: "go"}
	require.Error(t, Run(context.Background(), options, io.Discard, nil, nil))
}

func TestFixturePreparationAndDictionaryCorruption(t *testing.T) {
	t.Parallel()
	c, _ := fakeController(t, runtime.GOARCH)
	require.NoError(t, c.acquire())
	require.NoError(t, c.buildHelpers())
	for _, configuration := range []Configuration{Configurations()[0], Configurations()[3]} {
		l, err := c.pack(configuration)
		require.NoError(t, err)
		err = corruptBundle(l.Bundle, true)
		if configuration.Dictionary {
			require.NoError(t, err)
		} else {
			require.ErrorContains(t, err, "missing dictionary")
		}
	}
	require.Error(t, corruptBundle(filepath.Join(t.TempDir(), "missing"), false))
	for _, scenario := range []string{readOnlyWarm, corruptPayload} {
		item := caseFor(t, scenario, Auto)
		e := validEvidence(t, item)
		e.Request.Root = t.TempDir()
		c.reporter = filepath.Join(t.TempDir(), "absent")
		require.Error(t, configureScenario(c, item, e.Lineage, &e.Request, "unused"))
	}
	for _, mode := range []string{"file", "readonly"} {
		t.Run(mode, func(t *testing.T) {
			if os.Getuid() == 0 {
				t.Skip("permission errors require ordinary credentials")
			}
			c.options.Output = t.TempDir()
			path := filepath.Join(c.options.Output, "fixtures")
			if mode == "file" {
				require.NoError(t, os.WriteFile(path, nil, dataMode))
			} else {
				require.NoError(t, os.Mkdir(path, 0o500))
				t.Cleanup(func() { require.NoError(t, os.Chmod(path, privateMode)) })
			}
			_, err := c.request(Cases()[0], Lineage{})
			require.Error(t, err)
		})
	}
}

func TestExerciseCannotLoseIdentityOrCleanup(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"candidate-cli", "request", "digest", "cleanup"} {
		t.Run(failure, func(t *testing.T) {
			if os.Getuid() == 0 {
				t.Skip("permission errors require ordinary credentials")
			}
			c, f := fakeController(t, runtime.GOARCH)
			require.NoError(t, c.acquire())
			require.NoError(t, c.buildHelpers())
			c.summary.Expected = Cases()[:1]
			switch failure {
			case "candidate-cli":
				c.options.Input = Candidate
				c.summary.Expected = []Case{caseFor(t, distributedCLI, Cache)}
			case "request":
				require.NoError(t, os.Remove(c.reporter))
			case "digest":
				require.NoError(t, os.Chmod(c.reporter, 0))
			case "cleanup":
				c.runner.Execute = func(ctx context.Context, spec process.Spec) (releaseaudit.Result, error) {
					result, err := f.execute(ctx, spec)
					path := filepath.Join(c.options.Output, "fixtures")
					require.NoError(t, os.Chmod(path, 0o500))
					t.Cleanup(func() { require.NoError(t, os.Chmod(path, privateMode)) })
					return result, err
				}
			}
			require.Error(t, c.runCases(io.Discard))
		})
	}
}

func TestFailedWorkersAndEvidenceWritesCannotPass(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"malformed", testExit, "progress", "case-record", "summary"} {
		t.Run(failure, func(t *testing.T) {
			c, f := fakeController(t, runtime.GOARCH)
			require.NoError(t, c.acquire())
			require.NoError(t, c.buildHelpers())
			c.summary.Expected = Cases()[:1]
			c.runner.Execute = func(ctx context.Context, spec process.Spec) (releaseaudit.Result, error) {
				if failure == "malformed" {
					return releaseaudit.Result{Stdout: "{"}, nil
				}
				result, err := f.execute(ctx, spec)
				if failure == testExit {
					result.ExitCode = 125
				}
				return result, err
			}
			if failure == "case-record" || failure == "summary" {
				name := "summary.json"
				if failure == "case-record" {
					name = strings.ReplaceAll(c.summary.Expected[0].ID, "/", "__") + ".json"
				} else {
					require.NoError(t, os.Remove(filepath.Join(c.options.Output, name)))
				}
				require.NoError(t, os.Mkdir(filepath.Join(c.options.Output, name), privateMode))
			}
			var progress io.Writer = io.Discard
			if failure == "progress" {
				progress = brokenProgress{}
			}
			require.Error(t, c.runCases(progress))
			if failure == "malformed" || failure == testExit {
				require.Equal(t, Fail, c.summary.Results[0].Status)
				require.NotEmpty(t, c.summary.Results[0].Reason)
				require.DirExists(t, c.summary.Results[0].Request.Root, "a failed fixture must remain replayable")
			}
		})
	}
}
