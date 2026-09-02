// Package e2e_test implements black-box end-to-end integration tests for microfat.
package e2e_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

const (
	envDebugTrue          = "MICROFAT_DEBUG=1"
	envExecCache          = "MICROFAT_EXEC_MODE=cache"
	defaultFilePerm       os.FileMode = 0o755
	privateDirPerm        os.FileMode = 0o700
	privateFilePerm       os.FileMode = 0o600
	defaultExitCode       int         = 0
	bufferSize            int         = 4096
	dictSizeKB            int         = 112
	goldenAppPkg          string      = "./testdata/golden"
	seccompRunnerPkg      string      = "./testdata/seccomp_runner"
	cliPackagePath        string      = "../../cmd/microfat"
	stubPackagePath       string      = "../../cmd/microfat-stub"
	exitCodeUnknownError  int         = 1
	trailerMagicSizeBytes int64       = 8
)

// BinaryTrailer represents parsed 56-byte trailer fields at EOF.
type BinaryTrailer struct {
	IndexOffset int64
	IndexSize   int64
	IndexSHA256 [format.HashLen]byte
	Magic       string
}

var (
	e2eRootDir        string
	cliPath           string
	stubPath          string
	seccompRunnerPath string
	goldenVariantBins = make(map[string]string)
	goldenFatBin      string
	goldenDictFatBin  string
	currentHostArch   string
	currentHostLevel  string
)

func TestMain(m *testing.M) {
	if runtime.GOOS != "linux" {
		fmt.Println("[e2e] skipping E2E tests: linux target OS required for ELF microfat dispatch")
		os.Exit(0)
	}

	tempDir, err := os.MkdirTemp("", "microfat-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create E2E temp directory: %v\n", err)
		os.Exit(1)
	}
	e2eRootDir = tempDir

	exitCode := runSetupAndExecute(m)
	_ = os.RemoveAll(e2eRootDir)
	os.Exit(exitCode)
}

func runSetupAndExecute(m *testing.M) int {
	currentHostArch = runtime.GOARCH
	currentHostLevel = microarch.CurrentLevel()

	// 1. Compile microfat CLI
	cliPath = filepath.Join(e2eRootDir, "microfat-cli")
	if err := compileBinary(cliPackagePath, cliPath, nil); err != nil {
		fmt.Fprintf(os.Stderr, "failed to compile microfat CLI: %v\n", err)
		return 1
	}

	// 2. Compile microfat-stub
	stubPath = filepath.Join(e2eRootDir, "microfat-stub")
	stubEnv := []string{"GOAMD64=v1", "GOARM64=v8.0"}
	if err := compileBinary(stubPackagePath, stubPath, stubEnv); err != nil {
		fmt.Fprintf(os.Stderr, "failed to compile microfat-stub: %v\n", err)
		return 1
	}

	// 3. Compile seccomp runner helper
	seccompRunnerPath = filepath.Join(e2eRootDir, "seccomp-runner")
	if err := compileBinary(seccompRunnerPkg, seccompRunnerPath, nil); err != nil {
		fmt.Fprintf(os.Stderr, "failed to compile seccomp-runner: %v\n", err)
		return 1
	}

	// 4. Compile golden application variants
	variantsDir := filepath.Join(e2eRootDir, "variants")
	if err := os.MkdirAll(variantsDir, defaultFilePerm); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create variants directory: %v\n", err)
		return 1
	}

	var levelsToBuild []string
	switch currentHostArch {
	case microarch.ArchAMD64:
		levelsToBuild = []string{"v1", "v2", "v3", "v4"}
	case microarch.ArchARM64:
		levelsToBuild = []string{"v8.0", "v8.2", "v9.0"}
	default:
		levelsToBuild = []string{"v1"}
	}

	for _, lvl := range levelsToBuild {
		binName := "golden_" + lvl
		binPath := filepath.Join(variantsDir, binName)
		envKV := []string{}
		if currentHostArch == microarch.ArchAMD64 {
			envKV = append(envKV, "GOAMD64="+lvl)
		}
		ldflags := fmt.Sprintf("-ldflags=-s -w -X main.Variant=%s", lvl)
		if err := compileBinaryWithFlags(goldenAppPkg, binPath, envKV, ldflags); err != nil {
			fmt.Fprintf(os.Stderr, "failed to compile golden variant %s: %v\n", lvl, err)
			return 1
		}
		goldenVariantBins[lvl] = binPath
	}

	// 5. Pack baseline golden fat binary
	goldenFatBin = filepath.Join(e2eRootDir, "app-golden.fat")
	if err := packBinary(cliPath, stubPath, "golden-app", goldenFatBin, goldenVariantBins); err != nil {
		fmt.Fprintf(os.Stderr, "failed to pack baseline golden fat binary: %v\n", err)
		return 1
	}

	// 6. Pack shared dictionary golden fat binary
	goldenDictFatBin = filepath.Join(e2eRootDir, "app-golden-dict.fat")
	if err := packBinaryWithDict(cliPath, stubPath, "golden-dict-app", goldenDictFatBin, goldenVariantBins); err != nil {
		fmt.Fprintf(os.Stderr, "failed to pack dictionary golden fat binary: %v\n", err)
		return 1
	}

	return m.Run()
}

func compileBinary(pkg, outPath string, extraEnv []string) error {
	return compileBinaryWithFlags(pkg, outPath, extraEnv, "-ldflags=-s -w")
}

func compileBinaryWithFlags(pkg, outPath string, extraEnv []string, flags ...string) error {
	args := append([]string{"build", "-buildvcs=false"}, flags...)
	args = append(args, "-o", outPath, pkg)
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build %s (%s): %w\noutput: %s", pkg, outPath, err, out)
	}
	return nil
}

func packBinary(cli, stub, name, outPath string, variants map[string]string) error {
	return packBinaryCustom(cli, stub, name, outPath, variants, false)
}

func packBinaryWithDict(cli, stub, name, outPath string, variants map[string]string) error {
	return packBinaryCustom(cli, stub, name, outPath, variants, true)
}

func packBinaryCustom(cli, stub, name, outPath string, variants map[string]string, enableDict bool) error {
	args := []string{
		"pack",
		"--stub", stub,
		"--name", name,
		"-o", outPath,
	}
	if enableDict {
		args = append(args, "--dict")
	}
	for lvl, path := range variants {
		args = append(args, "-v", fmt.Sprintf("%s=%s", lvl, path))
	}
	cmd := exec.Command(cli, args...)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pack command failed: %w\noutput: %s", err, out)
	}
	return nil
}

func executeFatBinary(t testing.TB, binPath string, env []string, args ...string) (string, string, int, error) {
	t.Helper()
	var stdoutBuf, stderrBuf bytes.Buffer
	var err error

	const maxAttempts = 10
	const retryBackoff = 10 * time.Millisecond

	for attempt := 0; attempt < maxAttempts; attempt++ {
		cmd := exec.Command(binPath, args...)
		if len(env) > 0 {
			cmd.Env = append(os.Environ(), env...)
		}
		stdoutBuf.Reset()
		stderrBuf.Reset()
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf

		err = cmd.Run()
		if err != nil && strings.Contains(err.Error(), "text file busy") {
			time.Sleep(retryBackoff)
			continue
		}
		break
	}

	exitCode := defaultExitCode
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = exitCodeUnknownError
		}
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode, err
}

func executeWithSeccompBlockedMemfd(t testing.TB, binPath string, env []string, args ...string) (string, string, int, error) {
	t.Helper()
	runnerArgs := append([]string{binPath}, args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	var err error

	const maxAttempts = 10
	const retryBackoff = 10 * time.Millisecond

	for attempt := 0; attempt < maxAttempts; attempt++ {
		cmd := exec.Command(seccompRunnerPath, runnerArgs...)
		if len(env) > 0 {
			cmd.Env = append(os.Environ(), env...)
		}
		stdoutBuf.Reset()
		stderrBuf.Reset()
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf

		err = cmd.Run()
		if err != nil && strings.Contains(err.Error(), "text file busy") {
			time.Sleep(retryBackoff)
			continue
		}
		break
	}

	exitCode := defaultExitCode
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = exitCodeUnknownError
		}
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode, err
}

func assertSelectedMatchesExecuted(t testing.TB, stdout, stderr, expectedVariant string) {
	t.Helper()
	expectedOutput := "golden:variant=" + expectedVariant
	if !strings.Contains(stdout, expectedOutput) {
		t.Fatalf("expected application executed variant %q in stdout, got:\n%s", expectedOutput, stdout)
	}
	expectedLog := "selected_variant=" + expectedVariant
	if !strings.Contains(stderr, expectedLog) && !strings.Contains(stderr, "selected variant: "+expectedVariant) {
		t.Fatalf("expected launcher selected variant %q in stderr telemetry, got:\n%s", expectedLog, stderr)
	}
}

func copyFile(t testing.TB, src, dst string) int64 {
	t.Helper()
	srcFile, err := os.Open(src)
	if err != nil {
		t.Fatalf("opening source file %s: %v", src, err)
	}
	defer func() { _ = srcFile.Close() }()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, defaultFilePerm)
	if err != nil {
		t.Fatalf("creating destination file %s: %v", dst, err)
	}

	n, err := io.Copy(dstFile, srcFile)
	if err != nil {
		_ = dstFile.Close()
		t.Fatalf("copying from %s to %s: %v", src, dst, err)
	}

	if err := dstFile.Sync(); err != nil {
		_ = dstFile.Close()
		t.Fatalf("syncing destination file %s: %v", dst, err)
	}
	if err := dstFile.Close(); err != nil {
		t.Fatalf("closing destination file %s: %v", dst, err)
	}

	return n
}

func mutateFileBytes(t testing.TB, path string, offset int64, patch []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s for mutation: %v", path, err)
	}

	if _, err := f.WriteAt(patch, offset); err != nil {
		_ = f.Close()
		t.Fatalf("writing mutation patch at offset %d in %s: %v", offset, path, err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		t.Fatalf("syncing %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing %s: %v", path, err)
	}
}

func readTrailerAndIndex(t testing.TB, binPath string) (BinaryTrailer, *format.Index) {
	t.Helper()
	f, err := os.Open(binPath)
	if err != nil {
		t.Fatalf("opening binary %s: %v", binPath, err)
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat binary %s: %v", binPath, err)
	}

	trailerBuf := make([]byte, format.TrailerSize)
	if _, err := f.ReadAt(trailerBuf, fi.Size()-format.TrailerSize); err != nil {
		t.Fatalf("reading trailer from %s: %v", binPath, err)
	}

	var bt BinaryTrailer
	bt.IndexOffset = int64(binary.LittleEndian.Uint64(trailerBuf[0:format.OffsetLen]))
	bt.IndexSize = int64(binary.LittleEndian.Uint64(trailerBuf[format.OffsetLen : format.OffsetLen+format.SizeLen]))
	copy(bt.IndexSHA256[:], trailerBuf[format.OffsetLen+format.SizeLen:format.OffsetLen+format.SizeLen+format.HashLen])
	bt.Magic = string(trailerBuf[format.TrailerSize-format.MagicLen : format.TrailerSize])

	idx, err := format.ReadTrailerAndIndex(f, fi.Size())
	if err != nil {
		t.Fatalf("reading trailer and index from %s: %v", binPath, err)
	}

	return bt, idx
}
