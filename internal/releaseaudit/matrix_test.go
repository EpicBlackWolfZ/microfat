package releaseaudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/stretchr/testify/require"
)

// The fake product is an independent contract oracle: validate the commands and
// supply observable outputs/files without executing any downloaded product.
const detectCommand = "detect"
const inspectCommand = "inspect"

type productFake struct {
	t       *testing.T
	options Options
	level   string
	calls   int
	failAt  int
	change  func(process.Spec, *Result)
}

func (f *productFake) execute(_ context.Context, spec process.Spec) (Result, error) {
	f.calls++
	if f.calls == f.failAt {
		return Result{}, errors.New("injected command failure")
	}
	result := Result{}
	switch filepath.Base(spec.Path) {
	case "cosign":
		require.Equal(f.t, "verify-blob", spec.Args[0])
	case "go":
		if spec.Args[0] == "version" {
			result.Stdout = "go version go1.27.1 linux/" + f.options.Arch
		} else {
			require.Equal(f.t, "build", spec.Args[0])
			require.Contains(f.t, spec.Args, "-buildvcs=false")
			require.Contains(f.t, spec.Env, "CGO_ENABLED=0")
			require.Contains(f.t, spec.Env, "GOARCH="+f.options.Arch)
		}
	case "gofmt":
		require.Equal(f.t, "-w", spec.Args[0])
	case cliName:
		f.cli(spec, &result)
	case "corrupt":
		result.ExitCode = 1
	case "native":
		result.Stdout = `{"arch":"` + f.options.Arch + `"}`
	case "packed":
		f.packed(spec, &result)
	default:
		f.t.Fatalf("unexpected command: %+v", spec)
	}
	if f.change != nil {
		f.change(spec, &result)
	}
	return result, nil
}
func argument(spec process.Spec, key string) string {
	index := slices.Index(spec.Args, key)
	if index < 0 || index+1 >= len(spec.Args) {
		return ""
	}
	return spec.Args[index+1]
}
func (f *productFake) cli(spec process.Spec, result *Result) {
	switch spec.Args[0] {
	case "--version":
		result.Stdout = "microfat version " + f.options.Version + " (commit)"
	case detectCommand:
		result.Stdout = fmt.Sprintf(`{"arch":%q,"level":%q}`, f.options.Arch, f.level)
	case "pack":
		packed := argument(spec, "-o")
		item := filepath.Base(filepath.Dir(packed))
		stub := argument(spec, "--stub")
		if item == "v2-full-none-dict0" {
			require.Empty(f.t, stub)
		} else {
			require.NotEmpty(f.t, stub)
		}
		if strings.HasSuffix(item, "dict1") {
			require.Contains(f.t, spec.Args, "--dict")
		}
		require.Equal(f.t, f.options.Arch, argument(spec, "--arch"))
		write(f.t, packed, "xxpayload")
	case inspectCommand:
		version := 1
		if strings.HasPrefix(filepath.Base(filepath.Dir(spec.Args[1])), "v2-") {
			version = 2
		}
		result.Stdout = fmt.Sprintf(`{"version":%d,"arch":%q,"variants":[{"offset":2},{"offset":3}]}`, version, f.options.Arch)
	case "verify":
		result.Stdout = `{"valid":true}`
		if filepath.Base(spec.Args[1]) == "corrupt" {
			result.ExitCode = 1
			result.Stdout = `{"valid":false}`
		}
	default:
		f.t.Fatalf("unexpected CLI command: %+v", spec)
	}
}
func (f *productFake) packed(spec process.Spec, result *Result) {
	switch spec.Args[0] {
	case "--microfat:help":
		result.ExitCode = 1
	case "--microfat:info":
		result.Stdout = "information"
	case "--microfat:prewarm=all,json":
		cache := filepath.Join(filepath.Dir(spec.Path), "prewarm")
		require.NoError(f.t, os.Mkdir(cache, directoryMode))
		write(f.t, filepath.Join(cache, "payload"), "bytes")
	case "--microfat:prewarm=all,verify,json":
		result.Stdout = `{"valid":true}`
	default:
		if strings.HasPrefix(spec.Args[0], "--microfat:optimize-to=") {
			return
		}
		require.Equal(f.t, []string{"argument with spaces", "*literal*"}, spec.Args)
		data, err := json.Marshal(map[string]any{"arch": f.options.Arch, "variant": f.level, "origin": spec.Path, "args": spec.Args})
		require.NoError(f.t, err)
		result.Stdout = string(data)
	}
}
func setupFake(t *testing.T, arch string) (Options, Runner, string, *productFake) {
	t.Helper()
	options := optionsFor(t)
	options.Arch = arch
	require.NoError(t, os.Mkdir(options.Output, directoryMode))
	products := filepath.Join(options.Output, "products")
	require.NoError(t, os.Mkdir(products, directoryMode))
	for _, name := range []string{cliName, fullStub, minimalStub} {
		write(t, filepath.Join(products, name), "x")
	}
	level := "v3"
	if arch == "arm64" {
		level = "v8.2"
	}
	fake := &productFake{t: t, options: options, level: level}
	runner := Runner{Output: options.Output, Execute: fake.execute}
	return options, runner, products, fake
}
func TestCompleteNativeMatrix(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()
			options, runner, products, fake := setupFake(t, arch)
			var progress strings.Builder
			require.NoError(t, matrix(options, runner, products, &progress))
			var results []caseResult
			data, err := os.ReadFile(filepath.Join(options.Output, "results.json"))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &results))
			require.Len(t, results, 16)
			require.Equal(t, 16, strings.Count(progress.String(), "passed"))
			names := map[string]bool{}
			for _, result := range results {
				require.False(t, names[result.Case])
				names[result.Case] = true
				require.Equal(t, "pass", result.Status)
				require.Len(t, result.Digest, 64)
			}
			require.Greater(t, fake.calls, 200)
			corrupt, err := os.ReadFile(filepath.Join(options.Output, "v1-full-none-dict0", "corrupt"))
			require.NoError(t, err)
			require.Equal(t, byte('p')^0xff, corrupt[2])
			require.Equal(t, byte('a')^0xff, corrupt[3])
		})
	}
}

func TestEveryFirstCaseCommandFailureStopsAudit(t *testing.T) {
	t.Parallel()
	// Include preparation, full operations, corruption and the following minimal
	// cases, ensuring command errors are never mistaken for evidence of a pass.
	const commandsToFail = 100
	for count := 1; count <= commandsToFail; count++ {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			options, runner, products, fake := setupFake(t, "amd64")
			fake.failAt = count
			require.ErrorContains(t, matrix(options, runner, products, io.Discard), "injected command failure")
			require.Equal(t, count, fake.calls)
		})
	}
}

func TestMatrixRejectsWrongObservations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, command, response string }{
		{"wrong version", "--version", "0.2.40"}, {"empty detect", detectCommand, `{}`},
		{"wrong arch", detectCommand, `{"arch":"arm64","level":"v8.0"}`},
		{"unknown level", detectCommand, `{"arch":"amd64","level":"../../escape"}`},
		{"invalid json", detectCommand, "not json"}, {"wrong format", inspectCommand, `{"version":2,"arch":"amd64"}`},
		{"wrong inspected arch", inspectCommand, `{"version":1,"arch":"arm64"}`},
		{"missing variants", inspectCommand, `{"version":1,"arch":"amd64"}`},
		{"negative offset", inspectCommand, `{"version":1,"arch":"amd64","variants":[{"offset":-1}]}`},
		{"large offset", inspectCommand, `{"version":1,"arch":"amd64","variants":[{"offset":200}]}`},
		{"wrong variant", "argument with spaces", `{"arch":"amd64","variant":"v1"}`},
		{"wrong args", "argument with spaces", `{"arch":"amd64","variant":"v3","args":[]}`},
		{"wrong origin", "argument with spaces", `{"arch":"amd64","variant":"v3","args":["argument with spaces","*literal*"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options, runner, products, fake := setupFake(t, "amd64")
			fake.change = func(spec process.Spec, result *Result) {
				if len(spec.Args) > 0 && spec.Args[0] == test.command {
					result.Stdout = test.response
				}
			}
			require.Error(t, matrix(options, runner, products, io.Discard))
		})
	}
}

func TestCacheAndOptimizedPayloadValidation(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"changed bytes", "changed mode", "changed inventory",
		"missing cache", "wrong native", "invalid native json"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			options, runner, products, fake := setupFake(t, "amd64")
			fake.change = func(spec process.Spec, result *Result) {
				if filepath.Base(spec.Path) == "native" {
					if failure == "wrong native" {
						result.Stdout = `{"arch":"arm64"}`
					}
					if failure == "invalid native json" {
						result.Stdout = "broken"
					}
					return
				}
				if len(spec.Args) == 0 || spec.Args[0] != "--microfat:prewarm=all,verify,json" {
					return
				}
				cache := filepath.Join(filepath.Dir(spec.Path), "prewarm")
				filename := filepath.Join(cache, "payload")
				switch failure {
				case "changed bytes":
					write(t, filename, "changed")
				case "changed mode":
					require.NoError(t, os.Chmod(filename, 0o755))
				case "changed inventory":
					write(t, filepath.Join(cache, "extra"), "extra")
				case "missing cache":
					require.NoError(t, os.RemoveAll(cache))
				}
			}
			require.Error(t, matrix(options, runner, products, io.Discard))
		})
	}
}

func TestAuditOrchestration(t *testing.T) {
	t.Parallel()
	options := optionsFor(t)
	signedFixture(t, options)
	level := "v1"
	if options.Arch == "arm64" {
		level = "v8.0"
	}
	fake := &productFake{t: t, options: options, level: level}
	require.NoError(t, Run(context.Background(), options, io.Discard, nil, fake.execute))
	data, err := os.ReadFile(filepath.Join(options.Output, "verification.json"))
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, json.Unmarshal(data, &record))
	require.Equal(t, options.Source, record["source"])
	require.Error(t, Run(context.Background(), options, io.Discard, nil, fake.execute), "cannot reuse prior evidence directory")
	for _, test := range []string{"invalid options", "invalid output parent", "signature", "extraction",
		"go version failure", "wrong toolchain", "record failure", "hash failure"} {
		t.Run(test, func(t *testing.T) {
			t.Parallel()
			options := optionsFor(t)
			signedFixture(t, options)
			fake := &productFake{t: t, options: options, level: level}
			switch test {
			case "invalid options":
				options.Source = "bad"
			case "invalid output parent":
				options.Output = filepath.Join(options.Dist, "checksums.txt", "out")
			case "signature":
				fake.failAt = 1
			case "extraction":
				fake.change = func(spec process.Spec, _ *Result) {
					if spec.Path == "cosign" {
						require.NoError(t, os.Mkdir(options.Output+"/products", directoryMode))
					}
				}
			case "go version failure":
				fake.failAt = 2
			case "wrong toolchain":
				fake.change = func(spec process.Spec, r *Result) {
					if spec.Path == "go" {
						r.Stdout = "go version go1.26.0 linux/amd64"
					}
				}
			case "record failure":
				fake.change = func(spec process.Spec, _ *Result) {
					if spec.Path == "go" {
						require.NoError(t, os.Mkdir(options.Output+"/verification.json", directoryMode))
					}
				}
			case "hash failure":
				fake.change = func(spec process.Spec, _ *Result) {
					if spec.Path == "go" {
						require.NoError(t, os.Remove(options.Output+"/products/microfat"))
					}
				}
			}
			require.Error(t, Run(context.Background(), options, io.Discard, nil, fake.execute))
		})
	}
}
