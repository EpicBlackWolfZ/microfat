package releaseaudit

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/stretchr/testify/require"
)

const testProduct = "product"

func write(t *testing.T, filename, value string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filename, []byte(value), fileMode))
}
func optionsFor(t *testing.T) Options {
	t.Helper()
	return Options{Dist: t.TempDir(), Output: filepath.Join(t.TempDir(), "audit"), Version: "0.2.4",
		Source: strings.Repeat("a", 40), Arch: runtime.GOARCH}
}
func executor(result Result, err error) Execute {
	return func(context.Context, process.Spec) (Result, error) { return result, err }
}
func runnerFor(t *testing.T, execute Execute) Runner {
	t.Helper()
	return Runner{Output: t.TempDir(), Execute: execute}
}

func TestCommandFailureBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name              string
		result            Result
		err               error
		success, accepted bool
	}{
		{name: "success", success: true, accepted: true}, {name: "rejection", result: Result{ExitCode: 1}, accepted: true},
		{name: "unexpected success"}, {name: "unexpected rejection", result: Result{ExitCode: 1}, success: true},
		{name: "signal", result: Result{ExitCode: -1}}, {name: "panic", result: Result{ExitCode: 2, Stderr: "panic: bad index"}},
		{name: "fatal", result: Result{ExitCode: 2, Stderr: "fatal error: memory"}},
		{name: "partial command", result: Result{ExitCode: 0}, err: process.ErrOutputLimit, success: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := runnerFor(t, executor(test.result, test.err))
			_, err := runner.Run([]string{testProduct}, nil, test.success)
			require.Equal(t, test.accepted, err == nil)
			data, err := os.ReadFile(filepath.Join(runner.Output, "commands.jsonl"))
			require.NoError(t, err)
			var record CommandRecord
			require.NoError(t, json.Unmarshal(data, &record))
			require.Equal(t, test.result.ExitCode, record.Exit)
		})
	}
	runner := runnerFor(t, executor(Result{}, nil))
	_, err := runner.Run(nil, nil, true)
	require.Error(t, err)
	runner.Output = filepath.Join(t.TempDir(), "absent")
	_, err = runner.Run([]string{testProduct}, nil, true)
	require.Error(t, err)
	runner.Output = t.TempDir()
	require.NoError(t, os.Symlink("/dev/full", filepath.Join(runner.Output, "commands.jsonl")))
	_, err = runner.Run([]string{testProduct}, nil, true)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner = runnerFor(t, executor(Result{}, nil))
	runner.Context = ctx
	_, err = runner.Run([]string{testProduct}, nil, true)
	require.ErrorIs(t, err, context.Canceled)
}

func TestEnvironmentAndProcess(t *testing.T) {
	t.Parallel()
	env := []string{"PATH=/bin", "MICROFAT_CACHE_DIR=untrusted", "MICROFAT_EXEC_MODE=cache",
		"GOGC=off", "GOMEMLIMIT=1", "GOMAXPROCS=2", "KEEP=yes"}
	require.Equal(t, []string{"PATH=/bin", "KEEP=yes"}, CleanEnvironment(env))
	runner := runnerFor(t, func(_ context.Context, spec process.Spec) (Result, error) {
		require.ElementsMatch(t, []string{"PATH=/bin", "KEEP=replaced", "NEW=value"}, spec.Env)
		return Result{Stdout: "passed"}, nil
	})
	runner.Environment = CleanEnvironment(env)
	output, err := runner.Run([]string{testProduct, "arg"}, map[string]string{"KEEP": "replaced", "NEW": "value"}, true)
	require.NoError(t, err)
	require.Equal(t, "passed", output)
	for _, code := range []string{"0", "1"} {
		result, err := ExecuteProcess(context.Background(), process.Spec{Path: "/bin/sh",
			Args: []string{"-c", "printf output; printf err >&2; exit " + code}})
		require.NoError(t, err)
		require.Equal(t, "output", result.Stdout)
		require.Equal(t, "err", result.Stderr)
	}
	_, err = ExecuteProcess(context.Background(), process.Spec{Path: "/nonexistent/release-audit-test"})
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ExecuteProcess(ctx, process.Spec{Path: "/bin/sh",
		Args: []string{"-c", "exit 0"}})
	require.Error(t, err)
	require.True(t, onlyExitStatus(nil))
	require.True(t, onlyExitStatus(&exec.ExitError{}))
	require.False(t, onlyExitStatus(errors.Join(&exec.ExitError{}, process.ErrOutputLimit)))
	require.False(t, onlyExitStatus(fmt.Errorf("wrapped: %w", &exec.ExitError{})))
}

func makeArchive(t *testing.T, options Options, entries []*tar.Header) {
	t.Helper()
	file, err := os.Create(filepath.Join(options.Dist, "microfat_"+options.Version+"_linux_"+options.Arch+".tar.gz"))
	require.NoError(t, err)
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		require.NoError(t, writer.WriteHeader(entry))
		if entry.Size > 0 {
			_, err = io.CopyN(writer, strings.NewReader(strings.Repeat("x", int(entry.Size))), entry.Size)
			require.NoError(t, err)
		}
	}
	require.NoError(t, writer.Close())
	require.NoError(t, gz.Close())
	require.NoError(t, file.Close())
}
func productHeaders() []*tar.Header {
	var headers []*tar.Header
	for _, name := range []string{cliName, fullStub, minimalStub, "README.md", "LICENSE", "SECURITY.md"} {
		headers = append(headers, &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: 1})
	}
	return headers
}
func signedFixture(t *testing.T, options Options) {
	t.Helper()
	makeArchive(t, options, productHeaders())
	var rows []string
	for _, arch := range []string{"amd64", "arm64"} {
		base := "microfat_" + options.Version + "_linux_" + arch + ".tar.gz"
		for _, suffix := range []string{"", ".spdx.json", ".cyclonedx.json"} {
			filename := filepath.Join(options.Dist, base+suffix)
			switch suffix {
			case ".spdx.json":
				write(t, filename, `{"spdxVersion":"SPDX-2.3"}`)
			case ".cyclonedx.json":
				write(t, filename, `{"bomFormat":"CycloneDX","specVersion":"1.5"}`)
			default:
				if arch != options.Arch {
					write(t, filename, "archive")
				}
			}
			hash, err := Digest(filename)
			require.NoError(t, err)
			rows = append(rows, hash+"  "+base+suffix)
		}
	}
	write(t, filepath.Join(options.Dist, "checksums.txt"), strings.Join(rows, "\n")+"\n")
}

func TestAuthenticationPrecedesProducts(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"", "signature", "missing", "duplicate", "tampered", "symlink", "schema", "uppercase", "syntax"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			options := optionsFor(t)
			signedFixture(t, options)
			checksums := filepath.Join(options.Dist, "checksums.txt")
			data, err := os.ReadFile(checksums)
			require.NoError(t, err)
			rows := strings.Split(strings.TrimSpace(string(data)), "\n")
			asset := filepath.Join(options.Dist, "microfat_0.2.4_linux_amd64.tar.gz")
			switch failure {
			case "missing":
				rows = rows[1:]
			case "duplicate":
				rows[len(rows)-1] = rows[0]
			case "tampered":
				write(t, asset, "changed")
			case "symlink":
				require.NoError(t, os.Remove(asset))
				write(t, asset+".target", "archive")
				require.NoError(t, os.Symlink(asset+".target", asset))
			case "schema":
				asset = filepath.Join(options.Dist, "microfat_0.2.4_linux_amd64.tar.gz.cyclonedx.json")
				write(t, asset, `{"spdxVersion":"SPDX-2.3"}`)
				hash, err := Digest(asset)
				require.NoError(t, err)
				rows[2] = hash + "  " + filepath.Base(asset)
			case "uppercase":
				rows[0] = strings.ToUpper(rows[0][:64]) + rows[0][64:]
			case "syntax":
				rows[0] = "not a checksum"
			}
			write(t, checksums, strings.Join(rows, "\n")+"\n")
			calls := 0
			runner := runnerFor(t, func(_ context.Context, spec process.Spec) (Result, error) {
				calls++
				require.Equal(t, "cosign", spec.Path)
				require.Contains(t, spec.Args, "https://github.com/"+Repository+"/.github/workflows/release.yml@refs/tags/v0.2.4")
				require.Contains(t, spec.Args, options.Source)
				require.Contains(t, spec.Args, "https://token.actions.githubusercontent.com")
				if failure == "signature" {
					return Result{}, errors.New("signature rejected")
				}
				return Result{}, nil
			})
			verified, err := Authenticate(options, runner)
			if failure == "" {
				require.NoError(t, err)
				require.Len(t, verified, 6)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, 1, calls)
			_, err = os.Stat(filepath.Join(options.Output, "products"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestArchiveTrustBoundary(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		header    *tar.Header
		duplicate bool
	}{
		{name: "traversal", header: &tar.Header{Name: "../escape", Typeflag: tar.TypeReg}},
		{name: "absolute", header: &tar.Header{Name: "/absolute", Typeflag: tar.TypeReg}},
		{name: "link", header: &tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../escape"}},
		{name: "hard link", header: &tar.Header{Name: "link", Typeflag: tar.TypeLink, Linkname: "microfat"}},
		{name: "directory", header: &tar.Header{Name: "directory/", Typeflag: tar.TypeDir}},
		{name: "duplicate", header: &tar.Header{Name: "same", Typeflag: tar.TypeReg}, duplicate: true},
		{name: "backslash", header: &tar.Header{Name: `bad\path`, Typeflag: tar.TypeReg}},
		{name: "root", header: &tar.Header{Name: ".", Typeflag: tar.TypeReg}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := optionsFor(t)
			require.NoError(t, os.Mkdir(options.Output, directoryMode))
			entries := []*tar.Header{test.header}
			if test.duplicate {
				entries = append(entries, test.header)
			}
			makeArchive(t, options, entries)
			_, err := Extract(options)
			require.Error(t, err)
			children, err := os.ReadDir(filepath.Join(options.Output, "products"))
			require.NoError(t, err)
			require.Empty(t, children)
		})
	}
	options := optionsFor(t)
	require.NoError(t, os.Mkdir(options.Output, directoryMode))
	makeArchive(t, options, productHeaders())
	products, err := Extract(options)
	require.NoError(t, err)
	require.NoError(t, requiredProducts(products))
	_, err = Extract(options)
	require.Error(t, err, "existing output must never be overwritten")
	require.NoError(t, os.Chmod(filepath.Join(products, cliName), fileMode))
	require.Error(t, requiredProducts(products))
	require.NoError(t, os.Remove(filepath.Join(products, cliName)))
	require.Error(t, requiredProducts(products))
	require.NoError(t, os.Mkdir(filepath.Join(products, cliName), directoryMode))
	require.Error(t, requiredProducts(products))
	require.Error(t, preflight(filepath.Join(t.TempDir(), "absent")))
	bad := filepath.Join(t.TempDir(), "bad")
	write(t, bad, "not gzip")
	require.Error(t, preflight(bad))
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	_, err = gz.Write([]byte("not tar"))
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	require.NoError(t, os.WriteFile(bad, data.Bytes(), fileMode))
	require.Error(t, preflight(bad))
}

func TestMetadataAndOptions(t *testing.T) {
	t.Parallel()
	source := strings.Repeat("a", 40)
	valid := `{"tagName":"v0.2.4","isDraft":false,"isImmutable":true,"publishedAt":"2026-09-20T00:00:00Z"}`
	output, err := Metadata("v0.2.4", []byte(valid), source+"\n")
	require.NoError(t, err)
	require.Equal(t, "source="+source+"\nversion=0.2.4\n", output)
	for _, data := range []string{"{", `{}`, strings.Replace(valid, "false", "true", 1),
		strings.Replace(valid, `"isImmutable":true`, `"isImmutable":false`, 1),
		strings.Replace(valid, "v0.2.4", "v0.2.3", 1), strings.Replace(valid, "2026-09-20T00:00:00Z", "", 1)} {
		_, err = Metadata("v0.2.4", []byte(data), source)
		require.Error(t, err)
	}
	_, err = Metadata("v0.2.4", []byte(valid), source+"\nsource=evil")
	require.Error(t, err)
	_, err = Metadata("../../tag", []byte(valid), source)
	require.Error(t, err)
	options := optionsFor(t)
	require.NoError(t, options.Validate("linux", runtime.GOARCH))
	require.Error(t, options.Validate("darwin", runtime.GOARCH))
	require.Error(t, options.Validate("linux", "other"))
	options.Arch = "386"
	require.Error(t, options.Validate("linux", "386"))
	options = optionsFor(t)
	options.Source = "bad"
	require.Error(t, options.Validate("linux", runtime.GOARCH))
	options = optionsFor(t)
	options.Version = "v0.2.4"
	require.Error(t, options.Validate("linux", runtime.GOARCH))
	require.NoError(t, ValidateTag("v0.2.5-rc.1"))
	require.Error(t, ValidateTag("0.2.4"))
}
