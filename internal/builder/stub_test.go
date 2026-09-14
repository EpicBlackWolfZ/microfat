package builder

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

const (
	testArchAMD64      = "amd64"
	testArchARM64      = "arm64"
	dummyELFExtraBytes = 100
	mockMemfdTarget    = "/memfd:microfat_payload (deleted)"
)

func createDummyELF(t *testing.T, dir, name string, arch string) string {
	t.Helper()
	p := filepath.Join(dir, name)

	var elfHeader []byte
	switch arch {
	case testArchARM64:
		elfHeader = []byte{
			0x7f, 'E', 'L', 'F', 2, 1, 1, 0,
			0, 0, 0, 0, 0, 0, 0, 0,
			2, 0, 0xb7, 0x00, 1, 0, 0, 0,
		}
	default: // amd64
		elfHeader = []byte{
			0x7f, 'E', 'L', 'F', 2, 1, 1, 0,
			0, 0, 0, 0, 0, 0, 0, 0,
			2, 0, 0x3e, 0x00, 1, 0, 0, 0,
		}
	}

	payload := make([]byte, 0, len(elfHeader)+dummyELFExtraBytes)
	payload = append(payload, elfHeader...)
	payload = append(payload, make([]byte, dummyELFExtraBytes)...)
	if err := os.WriteFile(p, payload, 0o755); err != nil {
		t.Fatalf("failed to write dummy ELF %s: %v", p, err)
	}
	return p
}

type stubTestFixture struct {
	tmpDir        string
	cliStub       string
	manifestStub  string
	trustedBinDir string
	pathStub      string
	siblingDir    string
	siblingStub   string
	mockNativeDir func(dir string)
}

func setupStubTestFixture(t *testing.T) *stubTestFixture {
	t.Helper()
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", origPath) })

	tmpDir := t.TempDir()
	cliStub := createDummyELF(t, tmpDir, "cli-stub.elf", testArchAMD64)
	manifestStub := createDummyELF(t, tmpDir, "manifest-stub.elf", testArchAMD64)

	trustedBinDir := filepath.Join(tmpDir, "trusted-bin")
	if err := os.MkdirAll(trustedBinDir, 0o755); err != nil {
		t.Fatalf("mkdir trustedBinDir: %v", err)
	}
	pathStub := createDummyELF(t, trustedBinDir, "microfat-stub", testArchAMD64)
	if err := os.Chmod(pathStub, 0o755); err != nil {
		t.Fatalf("chmod pathStub: %v", err)
	}

	siblingDir := filepath.Join(tmpDir, "sibling-dir")
	if err := os.MkdirAll(siblingDir, 0o755); err != nil {
		t.Fatalf("mkdir siblingDir: %v", err)
	}
	siblingStub := createDummyELF(t, siblingDir, "microfat-stub", testArchAMD64)

	origOsExecutable := osExecutableFunc
	origReadlink := readlinkProcSelfExe
	t.Cleanup(func() {
		osExecutableFunc = origOsExecutable
		readlinkProcSelfExe = origReadlink
	})

	mockNativeDir := func(dir string) {
		dummyExe := filepath.Join(dir, "microfat")
		osExecutableFunc = func() (string, error) {
			return dummyExe, nil
		}
		readlinkProcSelfExe = func() (string, error) {
			return dummyExe, nil
		}
	}

	return &stubTestFixture{
		tmpDir:        tmpDir,
		cliStub:       cliStub,
		manifestStub:  manifestStub,
		trustedBinDir: trustedBinDir,
		pathStub:      pathStub,
		siblingDir:    siblingDir,
		siblingStub:   siblingStub,
		mockNativeDir: mockNativeDir,
	}
}

func TestResolveStubPath_ExplicitStubs(t *testing.T) {
	f := setupStubTestFixture(t)

	t.Run("Case1_AllSourcesExist_CLIPathSupplied_CLIWins", func(t *testing.T) {
		f.mockNativeDir(f.siblingDir)
		_ = os.Setenv("PATH", f.trustedBinDir)

		res, err := ResolveStubPath(f.cliStub, f.manifestStub, f.tmpDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res != f.cliStub {
			t.Fatalf("expected CLI stub %q, got %q", f.cliStub, res)
		}
	})

	t.Run("Case2_InvalidCLIPath_ValidFallbacksExist_ErrorNoFallback", func(t *testing.T) {
		f.mockNativeDir(f.siblingDir)
		_ = os.Setenv("PATH", f.trustedBinDir)

		invalidCLI := filepath.Join(f.tmpDir, "does-not-exist.elf")
		_, err := ResolveStubPath(invalidCLI, f.manifestStub, f.tmpDir)
		if err == nil {
			t.Fatalf("expected error for invalid CLI stub, got nil")
		}
		if !errors.Is(err, ErrStubNotFound) {
			t.Fatalf("expected ErrStubNotFound, got %v", err)
		}
	})

	t.Run("Case3_CLIOmitted_ExplicitManifestStub_ManifestWins", func(t *testing.T) {
		f.mockNativeDir(f.siblingDir)
		_ = os.Setenv("PATH", f.trustedBinDir)

		res, err := ResolveStubPath("", "manifest-stub.elf", f.tmpDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res != f.manifestStub {
			t.Fatalf("expected manifest stub %q, got %q", f.manifestStub, res)
		}
	})

	t.Run("Case4_InvalidManifestStub_ValidAutomaticCandidates_ErrorNoFallback", func(t *testing.T) {
		f.mockNativeDir(f.siblingDir)
		_ = os.Setenv("PATH", f.trustedBinDir)

		_, err := ResolveStubPath("", "nonexistent-manifest.elf", f.tmpDir)
		if err == nil {
			t.Fatalf("expected error for invalid manifest stub, got nil")
		}
		if !errors.Is(err, ErrStubNotFound) {
			t.Fatalf("expected ErrStubNotFound, got %v", err)
		}
	})

	t.Run("Case9_ExplicitProjectLocalStubSupplied_Allowed", func(t *testing.T) {
		res, err := ResolveStubPath(f.cliStub, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res != f.cliStub {
			t.Fatalf("expected %q, got %q", f.cliStub, res)
		}
	})

	t.Run("Case10_DirectoryOrSpecialFileCandidate_Rejected", func(t *testing.T) {
		dirCandidate := filepath.Join(f.tmpDir, "a-directory")
		_ = os.MkdirAll(dirCandidate, 0o755)

		_, err := ResolveStubPath(dirCandidate, "", "")
		if err == nil {
			t.Fatalf("expected error for directory candidate, got nil")
		}
		if !errors.Is(err, ErrStubNotFound) {
			t.Fatalf("expected ErrStubNotFound, got %v", err)
		}
	})
}

func TestResolveStubPath_SiblingAndRepoRelative(t *testing.T) {
	f := setupStubTestFixture(t)

	t.Run("Case4b_CLIOmitted_ManifestOmitted_SiblingStubWins", func(t *testing.T) {
		f.mockNativeDir(f.siblingDir)
		_ = os.Setenv("PATH", f.trustedBinDir)

		res, err := ResolveStubPath("", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res != f.siblingStub {
			t.Fatalf("expected sibling stub %q, got %q", f.siblingStub, res)
		}
	})

	t.Run("Case5_RepoRelativeBinAndParentBinExist_TrustedPATHWins", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		defer func() { _ = os.Chdir(cwd) }()

		sandbox := filepath.Join(f.tmpDir, "sandbox", "sub")
		if err := os.MkdirAll(sandbox, 0o755); err != nil {
			t.Fatalf("mkdir sandbox: %v", err)
		}
		localBin := filepath.Join(sandbox, "bin")
		parentBin := filepath.Join(f.tmpDir, "sandbox", "bin")
		_ = os.MkdirAll(localBin, 0o755)
		_ = os.MkdirAll(parentBin, 0o755)
		createDummyELF(t, localBin, "microfat-stub", testArchAMD64)
		createDummyELF(t, parentBin, "microfat-stub", testArchAMD64)

		if err := os.Chdir(sandbox); err != nil {
			t.Fatalf("chdir: %v", err)
		}

		emptyDir := filepath.Join(f.tmpDir, "empty-sibling")
		_ = os.MkdirAll(emptyDir, 0o755)
		f.mockNativeDir(emptyDir)

		_ = os.Setenv("PATH", f.trustedBinDir)

		res, err := ResolveStubPath("", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res != f.pathStub {
			t.Fatalf("expected PATH stub %q, got %q (repo-relative candidate must not be selected)", f.pathStub, res)
		}
	})

	t.Run("Case6_OnlyRepoRelativeBinExists_ReturnsError", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		defer func() { _ = os.Chdir(cwd) }()

		sandbox := filepath.Join(f.tmpDir, "sandbox2")
		localBin := filepath.Join(sandbox, "bin")
		_ = os.MkdirAll(localBin, 0o755)
		createDummyELF(t, localBin, "microfat-stub", testArchAMD64)

		if err := os.Chdir(sandbox); err != nil {
			t.Fatalf("chdir: %v", err)
		}

		emptyDir := filepath.Join(f.tmpDir, "empty-sibling2")
		_ = os.MkdirAll(emptyDir, 0o755)
		f.mockNativeDir(emptyDir)

		_ = os.Setenv("PATH", "")

		_, err = ResolveStubPath("", "", "")
		if err == nil {
			t.Fatalf("expected error when only repo-relative stub exists, got nil")
		}
		if !errors.Is(err, ErrStubNotFound) {
			t.Fatalf("expected ErrStubNotFound, got %v", err)
		}
	})
}

func TestResolveStubPath_PATHFilteringAndMinimal(t *testing.T) {
	f := setupStubTestFixture(t)

	t.Run("Case7_PATHContainsEmptyAndRelativeEntries_RelativeIgnoredTrustedWins", func(t *testing.T) {
		emptyDir := filepath.Join(f.tmpDir, "empty-sibling3")
		_ = os.MkdirAll(emptyDir, 0o755)
		f.mockNativeDir(emptyDir)

		craftedPath := strings.Join([]string{".", "bin", "", "../bin", f.trustedBinDir}, string(os.PathListSeparator))
		_ = os.Setenv("PATH", craftedPath)

		res, err := ResolveStubPath("", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res != f.pathStub {
			t.Fatalf("expected trusted absolute PATH stub %q, got %q", f.pathStub, res)
		}
	})

	t.Run("Case8_PATHEmpty_NoSibling_ClearError", func(t *testing.T) {
		emptyDir := filepath.Join(f.tmpDir, "empty-sibling4")
		_ = os.MkdirAll(emptyDir, 0o755)
		f.mockNativeDir(emptyDir)
		_ = os.Setenv("PATH", "")

		_, err := ResolveStubPath("", "", "")
		if err == nil {
			t.Fatalf("expected error when PATH is empty and no sibling exists, got nil")
		}
		if !errors.Is(err, ErrStubNotFound) {
			t.Fatalf("expected ErrStubNotFound, got %v", err)
		}
	})

	t.Run("Case11_SiblingMinimalStubPresent_FullAbsent_NoSilentSwitch", func(t *testing.T) {
		onlyMinimalDir := filepath.Join(f.tmpDir, "only-minimal")
		_ = os.MkdirAll(onlyMinimalDir, 0o755)
		createDummyELF(t, onlyMinimalDir, "microfat-stub-minimal", testArchAMD64)
		f.mockNativeDir(onlyMinimalDir)
		_ = os.Setenv("PATH", "")

		_, err := ResolveStubPath("", "", "")
		if err == nil {
			t.Fatalf("expected error when only microfat-stub-minimal is present, got nil")
		}
		if !errors.Is(err, ErrStubNotFound) {
			t.Fatalf("expected ErrStubNotFound, got %v", err)
		}
	})
}

func setupOriginMocks(t *testing.T) string {
	t.Helper()
	origOsExecutable := osExecutableFunc
	origEvalSymlinks := evalSymlinksFunc
	origReadlink := readlinkProcSelfExe
	origReadAndHash := readAndHashSelfExe
	origOpenFile := openFileFunc
	origResolveCache := resolveCacheDirFDFunc
	t.Cleanup(func() {
		osExecutableFunc = origOsExecutable
		evalSymlinksFunc = origEvalSymlinks
		readlinkProcSelfExe = origReadlink
		readAndHashSelfExe = origReadAndHash
		openFileFunc = origOpenFile
		resolveCacheDirFDFunc = origResolveCache
	})
	return t.TempDir()
}

func TestResolveInstallationDirectory_NativeExecution(t *testing.T) {
	tmpDir := setupOriginMocks(t)

	t.Run("NativeExecution_ReturnsDir_IgnoresForgedOriginalExe", func(t *testing.T) {
		binDir := filepath.Join(tmpDir, "app-bin")
		_ = os.MkdirAll(binDir, 0o755)
		appExe := filepath.Join(binDir, "microfat")
		_ = os.WriteFile(appExe, []byte("dummy-exe"), 0o755)

		osExecutableFunc = func() (string, error) { return appExe, nil }
		readlinkProcSelfExe = func() (string, error) { return appExe, nil }

		// Hostile environment variable set by caller
		_ = os.Setenv(format.EnvOriginalExe, "/malicious/injected/path")
		defer func() { _ = os.Unsetenv(format.EnvOriginalExe) }()

		dir, err := ResolveInstallationDirectory()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dir != binDir {
			t.Fatalf("expected native dir %q, got %q", binDir, dir)
		}
	})

	t.Run("NativeExecution_ReadlinkFails_FallsBackToExecutable", func(t *testing.T) {
		binDir := filepath.Join(tmpDir, "fallback-bin")
		_ = os.MkdirAll(binDir, 0o755)
		appExe := filepath.Join(binDir, "microfat")
		_ = os.WriteFile(appExe, []byte("dummy-exe"), 0o755)

		readlinkProcSelfExe = func() (string, error) {
			return "", errors.New("readlink denied")
		}
		osExecutableFunc = func() (string, error) {
			return appExe, nil
		}

		dir, err := ResolveInstallationDirectory()
		if err != nil {
			t.Fatalf("unexpected error on fallback: %v", err)
		}
		if dir != binDir {
			t.Fatalf("expected fallback dir %q, got %q", binDir, dir)
		}

		// Test osExecutableFunc error branch
		osExecutableFunc = func() (string, error) {
			return "", errors.New("executable fail")
		}
		_, err = ResolveInstallationDirectory()
		if err == nil {
			t.Fatalf("expected error when osExecutableFunc fails")
		}
	})

	t.Run("NativeExecution_SymlinkEvalError", func(t *testing.T) {
		binDir := filepath.Join(tmpDir, "symlink-err-bin")
		_ = os.MkdirAll(binDir, 0o755)
		appExe := filepath.Join(binDir, "microfat")
		_ = os.WriteFile(appExe, []byte("dummy-exe"), 0o755)

		osExecutableFunc = func() (string, error) { return appExe, nil }
		readlinkProcSelfExe = func() (string, error) { return appExe, nil }
		evalSymlinksFunc = func(string) (string, error) {
			return "", errors.New("cannot eval symlinks")
		}

		dir, err := ResolveInstallationDirectory()
		if err != nil {
			t.Fatalf("unexpected error when eval symlinks fails: %v", err)
		}
		if dir != binDir {
			t.Fatalf("expected %q, got %q", binDir, dir)
		}
	})
}

func TestResolveInstallationDirectory_DispatchedModes(t *testing.T) {
	tmpDir := setupOriginMocks(t)

	t.Run("DispatchedExecution_Memfd_ValidConsistency_ReturnsParentDir", func(t *testing.T) {
		installDir := filepath.Join(tmpDir, "installed-fat with spaces")
		_ = os.MkdirAll(installDir, 0o755)

		// Create real fat binary fixture in installDir
		v1Path := createDummyELF(t, tmpDir, "app_v1", testArchAMD64)
		v1Bytes, _ := os.ReadFile(v1Path)
		v1Hash := sha256.Sum256(v1Bytes)
		v1Digest := hex.EncodeToString(v1Hash[:])

		stubPath := createDummyELF(t, tmpDir, "stub.elf", testArchAMD64)
		fatPath := filepath.Join(installDir, "myapp-fat")

		packOpts := pack.DefaultOptions()
		packOpts.StubPath = stubPath
		packOpts.OutputPath = fatPath
		packOpts.AppName = "myapp"
		packOpts.Variants = map[string]string{"v1": v1Path}
		packOpts.SkipELFValidation = true

		if _, err := pack.Pack(packOpts); err != nil {
			t.Fatalf("failed to pack fixture fat binary: %v", err)
		}

		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return int64(len(v1Bytes)), v1Digest, nil
		}

		// Set valid dispatch environment
		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, strconv.FormatInt(int64(len(v1Bytes)), 10))
		_ = os.Setenv(format.EnvSelectedSHA256, v1Digest)
		_ = os.Setenv(format.EnvOriginalExe, fatPath)
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		dir, err := ResolveInstallationDirectory()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dir != installDir {
			t.Fatalf("expected install dir %q, got %q", installDir, dir)
		}
	})

	t.Run("DispatchedExecution_CacheMode", func(t *testing.T) {
		cacheDir := filepath.Join(tmpDir, "cache-dispatch-dir")
		_ = os.MkdirAll(cacheDir, 0o755)
		resolveCacheDirFDFunc = func(string) (int, string, error) {
			return -1, cacheDir, nil
		}
		readlinkProcSelfExe = func() (string, error) {
			return filepath.Join(cacheDir, "dispatched_payload"), nil
		}
		v1Path := createDummyELF(t, tmpDir, "app_v1_cache", testArchAMD64)
		v1Bytes, _ := os.ReadFile(v1Path)
		v1Hash := sha256.Sum256(v1Bytes)
		v1Digest := hex.EncodeToString(v1Hash[:])

		stubPath := createDummyELF(t, tmpDir, "stub_cache.elf", testArchAMD64)
		fatPath := filepath.Join(cacheDir, "app-fat")
		packOpts := pack.DefaultOptions()
		packOpts.StubPath = stubPath
		packOpts.OutputPath = fatPath
		packOpts.AppName = "app-cache"
		packOpts.Variants = map[string]string{"v1": v1Path}
		packOpts.SkipELFValidation = true
		_, err := pack.Pack(packOpts)
		if err != nil {
			t.Fatalf("pack: %v", err)
		}

		readAndHashSelfExe = func() (int64, string, error) {
			return int64(len(v1Bytes)), v1Digest, nil
		}

		_ = os.Setenv(format.EnvExecMode, format.ExecModeCache)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, strconv.FormatInt(int64(len(v1Bytes)), 10))
		_ = os.Setenv(format.EnvSelectedSHA256, v1Digest)
		_ = os.Setenv(format.EnvOriginalExe, fatPath)
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		dir, err := ResolveInstallationDirectory()
		if err != nil {
			t.Fatalf("cache mode unexpected error: %v", err)
		}
		if dir != cacheDir {
			t.Fatalf("expected cache dir %q, got %q", cacheDir, dir)
		}
	})

	t.Run("DispatchedExecution_DispatchModeFallback", func(t *testing.T) {
		binDir := filepath.Join(tmpDir, "fallback-mode-bin")
		_ = os.MkdirAll(binDir, 0o755)
		v1Path := createDummyELF(t, tmpDir, "app_v1_fallback", testArchAMD64)
		v1Bytes, _ := os.ReadFile(v1Path)
		v1Hash := sha256.Sum256(v1Bytes)
		v1Digest := hex.EncodeToString(v1Hash[:])

		stubPath := createDummyELF(t, tmpDir, "stub_fallback.elf", testArchAMD64)
		fatPath := filepath.Join(binDir, "app-fat")
		packOpts := pack.DefaultOptions()
		packOpts.StubPath = stubPath
		packOpts.OutputPath = fatPath
		packOpts.AppName = "app-fallback"
		packOpts.Variants = map[string]string{"v1": v1Path}
		packOpts.SkipELFValidation = true
		_, err := pack.Pack(packOpts)
		if err != nil {
			t.Fatalf("pack: %v", err)
		}

		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return int64(len(v1Bytes)), v1Digest, nil
		}

		_ = os.Unsetenv(format.EnvExecMode)
		_ = os.Setenv(format.EnvDispatchMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, strconv.FormatInt(int64(len(v1Bytes)), 10))
		_ = os.Setenv(format.EnvSelectedSHA256, v1Digest)
		_ = os.Setenv(format.EnvOriginalExe, fatPath)
		defer func() {
			_ = os.Unsetenv(format.EnvDispatchMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		dir, err := ResolveInstallationDirectory()
		if err != nil {
			t.Fatalf("dispatch mode fallback unexpected error: %v", err)
		}
		if dir != binDir {
			t.Fatalf("expected binDir %q, got %q", binDir, dir)
		}
	})
}

func TestResolveInstallationDirectory_DispatchedValidationErrors(t *testing.T) {
	tmpDir := setupOriginMocks(t)

	t.Run("DispatchedExecution_MismatchedPayloadDigest_ReturnsError", func(t *testing.T) {
		installDir := filepath.Join(tmpDir, "installed-fat-tampered")
		_ = os.MkdirAll(installDir, 0o755)

		v1Path := createDummyELF(t, tmpDir, "app_v1_tampered", testArchAMD64)
		v1Bytes, _ := os.ReadFile(v1Path)
		stubPath := createDummyELF(t, tmpDir, "stub_tampered.elf", testArchAMD64)
		fatPath := filepath.Join(installDir, "myapp-fat-tampered")

		packOpts := pack.DefaultOptions()
		packOpts.StubPath = stubPath
		packOpts.OutputPath = fatPath
		packOpts.AppName = "myapp"
		packOpts.Variants = map[string]string{"v1": v1Path}
		packOpts.SkipELFValidation = true

		if _, err := pack.Pack(packOpts); err != nil {
			t.Fatalf("pack: %v", err)
		}

		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		// Hash does not match claimed
		readAndHashSelfExe = func() (int64, string, error) {
			return int64(len(v1Bytes)), "0000000000000000000000000000000000000000000000000000000000000000", nil
		}

		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, strconv.FormatInt(int64(len(v1Bytes)), 10))
		_ = os.Setenv(format.EnvSelectedSHA256, "1111111111111111111111111111111111111111111111111111111111111111")
		_ = os.Setenv(format.EnvOriginalExe, fatPath)
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		_, err := ResolveInstallationDirectory()
		if err == nil {
			t.Fatalf("expected error when running payload digest mismatches claimed digest, got nil")
		}
	})

	t.Run("DispatchedExecution_NonAbsoluteOriginalExe_ReturnsError", func(t *testing.T) {
		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return 100, "1111111111111111111111111111111111111111111111111111111111111111", nil
		}

		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, "100")
		_ = os.Setenv(format.EnvSelectedSHA256, "1111111111111111111111111111111111111111111111111111111111111111")
		_ = os.Setenv(format.EnvOriginalExe, "relative/path/to/fat")
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		_, err := ResolveInstallationDirectory()
		if err == nil {
			t.Fatalf("expected error for non-absolute original exe, got nil")
		}
	})

	t.Run("DispatchedExecution_ValidationErrors", func(t *testing.T) {
		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return 100, "1111111111111111111111111111111111111111111111111111111111111111", nil
		}

		// Invalid mode
		_ = os.Setenv(format.EnvExecMode, "invalid-mode")
		_, err := ResolveInstallationDirectory()
		if err == nil {
			t.Fatalf("expected error for invalid mode")
		}

		// Missing variant
		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Unsetenv(format.EnvSelectedVariant)
		_, err = ResolveInstallationDirectory()
		if err == nil {
			t.Fatalf("expected error for missing variant")
		}

		// Invalid claimed size
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, "-1")
		_, err = ResolveInstallationDirectory()
		if err == nil {
			t.Fatalf("expected error for negative claimed size")
		}

		// Invalid claimed digest
		_ = os.Setenv(format.EnvSelectedSize, "100")
		_ = os.Setenv(format.EnvSelectedSHA256, "not-a-valid-sha")
		_, err = ResolveInstallationDirectory()
		if err == nil {
			t.Fatalf("expected error for invalid claimed sha256")
		}

		// Size mismatch
		_ = os.Setenv(format.EnvSelectedSHA256, "1111111111111111111111111111111111111111111111111111111111111111")
		_ = os.Setenv(format.EnvSelectedSize, "200")
		_, err = ResolveInstallationDirectory()
		if err == nil {
			t.Fatalf("expected error for size mismatch")
		}

		// Clean up
		_ = os.Unsetenv(format.EnvExecMode)
		_ = os.Unsetenv(format.EnvSelectedVariant)
		_ = os.Unsetenv(format.EnvSelectedSize)
		_ = os.Unsetenv(format.EnvSelectedSHA256)
	})

	t.Run("DispatchedExecution_MissingOriginalExe", func(t *testing.T) {
		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return 100, "1111111111111111111111111111111111111111111111111111111111111111", nil
		}

		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, "100")
		_ = os.Setenv(format.EnvSelectedSHA256, "1111111111111111111111111111111111111111111111111111111111111111")
		_ = os.Unsetenv(format.EnvOriginalExe)
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
		}()

		_, err := ResolveInstallationDirectory()
		if err == nil {
			t.Fatalf("expected error for missing original exe hint")
		}
	})

	t.Run("DispatchedExecution_OriginalExeIsDir", func(t *testing.T) {
		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return 100, "1111111111111111111111111111111111111111111111111111111111111111", nil
		}

		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, "100")
		_ = os.Setenv(format.EnvSelectedSHA256, "1111111111111111111111111111111111111111111111111111111111111111")
		_ = os.Setenv(format.EnvOriginalExe, tmpDir)
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		_, err := ResolveInstallationDirectory()
		if err == nil {
			t.Fatalf("expected error when original exe is a directory")
		}
	})
}

func TestResolveInstallationDirectory_FileSystemErrors(t *testing.T) {
	tmpDir := setupOriginMocks(t)

	t.Run("DispatchedExecution_EvalSymlinksError", func(t *testing.T) {
		origEval := evalSymlinksFunc
		defer func() { evalSymlinksFunc = origEval }()

		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return 100, strings.Repeat("a", 64), nil
		}
		evalSymlinksFunc = func(string) (string, error) {
			return "", errors.New("symlink evaluation failed")
		}

		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, "100")
		_ = os.Setenv(format.EnvSelectedSHA256, strings.Repeat("a", 64))
		_ = os.Setenv(format.EnvOriginalExe, "/some/symlink/path")
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		_, err := ResolveInstallationDirectory()
		if err == nil || !strings.Contains(err.Error(), "resolving symlinks") {
			t.Fatalf("expected symlinks error, got: %v", err)
		}
	})

	t.Run("DispatchedExecution_StatError", func(t *testing.T) {
		origEval := evalSymlinksFunc
		defer func() { evalSymlinksFunc = origEval }()

		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return 100, strings.Repeat("a", 64), nil
		}
		evalSymlinksFunc = func(p string) (string, error) {
			return p, nil
		}

		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, "100")
		_ = os.Setenv(format.EnvSelectedSHA256, strings.Repeat("a", 64))
		_ = os.Setenv(format.EnvOriginalExe, filepath.Join(tmpDir, "non_existent_binary_xyz"))
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		_, err := ResolveInstallationDirectory()
		if err == nil || !strings.Contains(err.Error(), "stat original executable") {
			t.Fatalf("expected stat error, got: %v", err)
		}
	})

	t.Run("DispatchedExecution_OpenFileError", func(t *testing.T) {
		origOpen := openFileFunc
		defer func() { openFileFunc = origOpen }()

		existingFile := filepath.Join(tmpDir, "existing_file")
		_ = os.WriteFile(existingFile, []byte("content"), 0o755)

		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return 100, strings.Repeat("a", 64), nil
		}
		openFileFunc = func(string) (*os.File, error) {
			return nil, errors.New("permission denied open")
		}

		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, "100")
		_ = os.Setenv(format.EnvSelectedSHA256, strings.Repeat("a", 64))
		_ = os.Setenv(format.EnvOriginalExe, existingFile)
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		_, err := ResolveInstallationDirectory()
		if err == nil || !strings.Contains(err.Error(), "opening original executable") {
			t.Fatalf("expected open error, got: %v", err)
		}
	})

	t.Run("DispatchedExecution_ReadTrailerError", func(t *testing.T) {
		nonFatFile := filepath.Join(tmpDir, "non_fat_file")
		_ = os.WriteFile(nonFatFile, []byte("not a fat binary trailer"), 0o755)

		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return 100, strings.Repeat("a", 64), nil
		}

		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, "100")
		_ = os.Setenv(format.EnvSelectedSHA256, strings.Repeat("a", 64))
		_ = os.Setenv(format.EnvOriginalExe, nonFatFile)
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		_, err := ResolveInstallationDirectory()
		if err == nil || !strings.Contains(err.Error(), "reading index") {
			t.Fatalf("expected index read error, got: %v", err)
		}
	})

	t.Run("DispatchedExecution_ReadSelfExeAndPayloadLimits", func(t *testing.T) {
		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}

		// Error reading self exe
		readAndHashSelfExe = func() (int64, string, error) {
			return 0, "", errors.New("read self failed")
		}
		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, "10")
		_ = os.Setenv(format.EnvSelectedSHA256, strings.Repeat("a", 64))

		_, err := ResolveInstallationDirectory()
		if err == nil || !strings.Contains(err.Error(), "reading running payload") {
			t.Fatalf("expected reading running payload error, got: %v", err)
		}

		// Claimed size exceeds max payload size
		readAndHashSelfExe = func() (int64, string, error) {
			return 10, strings.Repeat("a", 64), nil
		}
		_ = os.Setenv(format.EnvSelectedSize, strconv.FormatInt(format.MaxPayloadSize+1, 10))

		_, err = ResolveInstallationDirectory()
		if err == nil || !strings.Contains(err.Error(), "invalid claimed payload size") {
			t.Fatalf("expected claimed size too large error, got: %v", err)
		}

		_ = os.Unsetenv(format.EnvExecMode)
		_ = os.Unsetenv(format.EnvSelectedVariant)
		_ = os.Unsetenv(format.EnvSelectedSize)
		_ = os.Unsetenv(format.EnvSelectedSHA256)
	})
}

func TestResolveInstallationDirectory_IndexValidationErrors(t *testing.T) {
	tmpDir := setupOriginMocks(t)

	t.Run("DispatchedExecution_IndexMismatchErrors", func(t *testing.T) {
		fatDir := filepath.Join(tmpDir, "mismatched-fat-dir")
		_ = os.MkdirAll(fatDir, 0o755)

		wrongArch := "arm64"
		wrongLevel := "v8.0"
		if runtime.GOARCH == "arm64" {
			wrongArch = "amd64"
			wrongLevel = "v1"
		}

		fatWrongArch := filepath.Join(fatDir, "wrong_arch.fat")
		idx := &format.Index{
			Version:    format.FormatVersion2,
			TargetArch: wrongArch,
			Variants: []format.VariantEntry{
				{
					Level:            wrongLevel,
					Offset:           10,
					CompressedSize:   10,
					UncompressedSize: 10,
					SHA256:           strings.Repeat("a", 64),
					Compression:      "none",
				},
			},
		}
		var buf bytes.Buffer
		buf.Write(make([]byte, 20))
		_, err := format.WriteIndexAndTrailer(&buf, idx, 20)
		if err != nil {
			t.Fatalf("writing index: %v", err)
		}
		if err := os.WriteFile(fatWrongArch, buf.Bytes(), 0o755); err != nil {
			t.Fatalf("write file: %v", err)
		}

		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return 10, strings.Repeat("a", 64), nil
		}

		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, wrongLevel)
		_ = os.Setenv(format.EnvSelectedSize, "10")
		_ = os.Setenv(format.EnvSelectedSHA256, strings.Repeat("a", 64))
		_ = os.Setenv(format.EnvOriginalExe, fatWrongArch)
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		_, err = ResolveInstallationDirectory()
		if err == nil {
			t.Fatalf("expected error for mismatched target arch")
		}
		if !strings.Contains(err.Error(), "does not match host") {
			t.Fatalf("expected error containing 'does not match host', got: %v", err)
		}
	})

	t.Run("DispatchedExecution_VersionNotV2", func(t *testing.T) {
		v1FatFile := filepath.Join(tmpDir, "v1_format.fat")
		idxV1 := &format.Index{
			Version:    format.FormatVersion1,
			TargetArch: runtime.GOARCH,
			Variants: []format.VariantEntry{
				{
					Level:            "v1",
					Offset:           10,
					CompressedSize:   10,
					UncompressedSize: 10,
					SHA256:           strings.Repeat("a", 64),
					Compression:      "none",
				},
			},
		}
		var buf bytes.Buffer
		buf.Write(make([]byte, 20))
		_, err := format.WriteIndexAndTrailerWithVersion(&buf, idxV1, 20, format.FormatVersion1)
		if err != nil {
			t.Fatalf("writing v1 index: %v", err)
		}
		if err := os.WriteFile(v1FatFile, buf.Bytes(), 0o755); err != nil {
			t.Fatalf("write file: %v", err)
		}

		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return 10, strings.Repeat("a", 64), nil
		}

		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, "10")
		_ = os.Setenv(format.EnvSelectedSHA256, strings.Repeat("a", 64))
		_ = os.Setenv(format.EnvOriginalExe, v1FatFile)
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		_, err = ResolveInstallationDirectory()
		if err == nil || !strings.Contains(err.Error(), "is not v2") {
			t.Fatalf("expected version not v2 error, got: %v", err)
		}
	})

	t.Run("DispatchedExecution_VariantNotFound", func(t *testing.T) {
		validFat := filepath.Join(tmpDir, "valid_v2.fat")
		idxV2 := &format.Index{
			Version:    format.FormatVersion2,
			TargetArch: runtime.GOARCH,
			Variants: []format.VariantEntry{
				{
					Level:            "v1",
					Offset:           10,
					CompressedSize:   10,
					UncompressedSize: 10,
					SHA256:           strings.Repeat("a", 64),
					Compression:      "none",
				},
			},
		}
		var buf bytes.Buffer
		buf.Write(make([]byte, 20))
		_, err := format.WriteIndexAndTrailer(&buf, idxV2, 20)
		if err != nil {
			t.Fatalf("writing v2 index: %v", err)
		}
		if err := os.WriteFile(validFat, buf.Bytes(), 0o755); err != nil {
			t.Fatalf("write file: %v", err)
		}

		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}
		readAndHashSelfExe = func() (int64, string, error) {
			return 10, strings.Repeat("a", 64), nil
		}

		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v4_nonexistent")
		_ = os.Setenv(format.EnvSelectedSize, "10")
		_ = os.Setenv(format.EnvSelectedSHA256, strings.Repeat("a", 64))
		_ = os.Setenv(format.EnvOriginalExe, validFat)
		defer func() {
			_ = os.Unsetenv(format.EnvExecMode)
			_ = os.Unsetenv(format.EnvSelectedVariant)
			_ = os.Unsetenv(format.EnvSelectedSize)
			_ = os.Unsetenv(format.EnvSelectedSHA256)
			_ = os.Unsetenv(format.EnvOriginalExe)
		}()

		_, err = ResolveInstallationDirectory()
		if err == nil || !strings.Contains(err.Error(), "not found in original executable") {
			t.Fatalf("expected variant not found error, got: %v", err)
		}
	})

	t.Run("DispatchedExecution_IndexSizeAndDigestMismatches", func(t *testing.T) {
		validFat := filepath.Join(tmpDir, "valid_v2.fat")
		readlinkProcSelfExe = func() (string, error) {
			return mockMemfdTarget, nil
		}

		// Size mismatch in index
		readAndHashSelfExe = func() (int64, string, error) {
			return 15, strings.Repeat("a", 64), nil
		}
		_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		_ = os.Setenv(format.EnvSelectedVariant, "v1")
		_ = os.Setenv(format.EnvSelectedSize, "15")
		_ = os.Setenv(format.EnvSelectedSHA256, strings.Repeat("a", 64))
		_ = os.Setenv(format.EnvOriginalExe, validFat)

		_, err := ResolveInstallationDirectory()
		if err == nil || !strings.Contains(err.Error(), "size in index (10) does not match running payload (15)") {
			t.Fatalf("expected index size mismatch error, got: %v", err)
		}

		// Digest mismatch in index
		readAndHashSelfExe = func() (int64, string, error) {
			return 10, strings.Repeat("b", 64), nil
		}
		_ = os.Setenv(format.EnvSelectedSize, "10")
		_ = os.Setenv(format.EnvSelectedSHA256, strings.Repeat("b", 64))

		_, err = ResolveInstallationDirectory()
		if err == nil || !strings.Contains(err.Error(), "digest in index") {
			t.Fatalf("expected index digest mismatch error, got: %v", err)
		}

		_ = os.Unsetenv(format.EnvExecMode)
		_ = os.Unsetenv(format.EnvSelectedVariant)
		_ = os.Unsetenv(format.EnvSelectedSize)
		_ = os.Unsetenv(format.EnvSelectedSHA256)
		_ = os.Unsetenv(format.EnvOriginalExe)
	})
}

func TestDefaultReadAndHashProcSelfExe(t *testing.T) {
	n, digest, err := defaultReadAndHashProcSelfExe()
	if err != nil {
		t.Skipf("skipping /proc/self/exe test if not accessible: %v", err)
	}
	if n <= 0 || len(digest) != 64 {
		t.Fatalf("expected positive size and 64-char hex digest, got size %d, digest %q", n, digest)
	}
}
