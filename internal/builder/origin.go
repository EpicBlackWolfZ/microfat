package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

var (
	// Injected functions for test isolation and mocking.
	osExecutableFunc      = os.Executable
	evalSymlinksFunc      = filepath.EvalSymlinks
	readlinkProcSelfExe   = func() (string, error) { return os.Readlink("/proc/self/exe") }
	readAndHashSelfExe    = defaultReadAndHashProcSelfExe
	openFileFunc          = os.Open
	resolveCacheDirFDFunc = format.ResolveCacheDirFD
)

func defaultReadAndHashProcSelfExe() (int64, string, error) {
	f, err := os.Open("/proc/self/exe")
	if err != nil {
		return 0, "", fmt.Errorf("opening /proc/self/exe: %w", err)
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	// Bounded read up to format.MaxPayloadSize + 1 to detect excessive size.
	lr := io.LimitReader(f, format.MaxPayloadSize+1)
	n, err := io.Copy(hasher, lr)
	if err != nil {
		return 0, "", fmt.Errorf("reading /proc/self/exe: %w", err)
	}
	if n > format.MaxPayloadSize {
		return 0, "", fmt.Errorf("%w: payload exceeds limit %d", format.ErrPayloadTooLarge, format.MaxPayloadSize)
	}
	return n, hex.EncodeToString(hasher.Sum(nil)), nil
}

// ResolveInstallationDirectory determines the installation directory of the current microfat binary.
//
// For native CLI execution, it uses the physical target of the running executable.
// For dispatched execution (via Linux memfd_create or disk cache fallback), it consumes the
// format.EnvOriginalExe location hint and validates consistency against the running payload's
// size and digest, and the original fat binary's format v2 index.
//
// Security boundary: Matching payload bytes establishes consistency of the location hint for stub
// discovery; it does NOT establish producer authenticity or a new privileged trust boundary.
func ResolveInstallationDirectory() (string, error) {
	if runtime.GOOS != "linux" {
		exePath, err := osExecutableFunc()
		if err != nil {
			return "", fmt.Errorf("resolving executable path: %w", err)
		}
		realPath, err := evalSymlinksFunc(exePath)
		if err != nil {
			realPath = exePath
		}
		return filepath.Dir(realPath), nil
	}

	target, err := readlinkProcSelfExe()
	if err != nil {
		// Fallback to os.Executable if /proc/self/exe cannot be read
		exePath, err := osExecutableFunc()
		if err != nil {
			return "", fmt.Errorf("resolving executable path: %w", err)
		}
		realPath, err := evalSymlinksFunc(exePath)
		if err != nil {
			realPath = exePath
		}
		return filepath.Dir(realPath), nil
	}

	// Determine if the process is running as a dispatched payload
	isMemfd := strings.HasPrefix(target, "memfd:") || strings.HasPrefix(target, "/memfd:") ||
		strings.Contains(target, "memfd:") || strings.Contains(target, "(deleted)")

	var isCache bool
	if !isMemfd {
		dirFD, cacheDir, cacheErr := resolveCacheDirFDFunc("")
		if cacheErr == nil {
			if dirFD >= 0 {
				_ = os.NewFile(uintptr(dirFD), "cacheDir").Close()
			}
			if cacheDir != "" && strings.HasPrefix(target, cacheDir) {
				isCache = true
			}
		}
	}

	// If execution is native (neither memfd nor cache dispatch), use the real executable location.
	// Inherited MICROFAT_* environment variables must not override native execution.
	if !isMemfd && !isCache {
		exePath, err := osExecutableFunc()
		if err != nil {
			return "", fmt.Errorf("resolving executable path: %w", err)
		}
		realPath, err := evalSymlinksFunc(exePath)
		if err != nil {
			realPath = exePath
		}
		return filepath.Dir(realPath), nil
	}

	// Dispatched execution: validate the original executable location hint
	return resolveDispatchedInstallationDirectory()
}

func resolveDispatchedInstallationDirectory() (string, error) {
	variant, actualSize, actualDigest, err := validateDispatchedPayloadEnvironment()
	if err != nil {
		return "", err
	}
	origExe := os.Getenv(format.EnvOriginalExe)
	return verifyOriginalExecutableAndIndex(origExe, variant, actualSize, actualDigest)
}

func validateDispatchedPayloadEnvironment() (string, int64, string, error) {
	mode := os.Getenv(format.EnvExecMode)
	if mode == "" {
		mode = os.Getenv(format.EnvDispatchMode)
	}
	if mode != format.ExecModeMemfd && mode != format.ExecModeCache {
		return "", 0, "", fmt.Errorf("dispatched execution has unsupported or missing mode %q", mode)
	}

	variant := os.Getenv(format.EnvSelectedVariant)
	if variant == "" {
		return "", 0, "", errors.New("missing selected variant in dispatch environment")
	}

	claimedSizeStr := os.Getenv(format.EnvSelectedSize)
	claimedSize, err := strconv.ParseInt(claimedSizeStr, 10, 64)
	if err != nil || claimedSize <= 0 || claimedSize > format.MaxPayloadSize {
		return "", 0, "", fmt.Errorf("invalid claimed payload size %q", claimedSizeStr)
	}

	claimedDigest := os.Getenv(format.EnvSelectedSHA256)
	if !format.ValidateChecksum(claimedDigest) {
		return "", 0, "", fmt.Errorf("invalid claimed payload checksum %q", claimedDigest)
	}

	actualSize, actualDigest, err := readAndHashSelfExe()
	if err != nil {
		return "", 0, "", fmt.Errorf("reading running payload: %w", err)
	}
	if actualSize != claimedSize {
		return "", 0, "", fmt.Errorf("running payload size %d does not match claimed size %d", actualSize, claimedSize)
	}
	if !strings.EqualFold(actualDigest, claimedDigest) {
		return "", 0, "", fmt.Errorf("running payload digest %s does not match claimed digest %s", actualDigest, claimedDigest)
	}

	return variant, actualSize, actualDigest, nil
}

func verifyOriginalExecutableAndIndex(origExe, variant string, actualSize int64, actualDigest string) (string, error) {
	if origExe == "" {
		return "", errors.New("missing original executable path hint")
	}
	if !filepath.IsAbs(origExe) {
		return "", fmt.Errorf("original executable path %q is not absolute", origExe)
	}

	physicalOrigExe, err := evalSymlinksFunc(origExe)
	if err != nil {
		return "", fmt.Errorf("resolving symlinks for %q: %w", origExe, err)
	}

	fileStat, err := os.Stat(physicalOrigExe)
	if err != nil {
		return "", fmt.Errorf("stat original executable %q: %w", physicalOrigExe, err)
	}
	if fileStat.IsDir() || !fileStat.Mode().IsRegular() {
		return "", fmt.Errorf("original executable %q is not a regular file", physicalOrigExe)
	}

	f, err := openFileFunc(physicalOrigExe)
	if err != nil {
		return "", fmt.Errorf("opening original executable %q: %w", physicalOrigExe, err)
	}
	defer func() { _ = f.Close() }()

	idx, err := format.ReadTrailerAndIndex(f, fileStat.Size())
	if err != nil {
		return "", fmt.Errorf("reading index from %q: %w", physicalOrigExe, err)
	}
	if idx.Version != format.FormatVersion2 {
		return "", fmt.Errorf("original executable %q format version %d is not v2", physicalOrigExe, idx.Version)
	}
	if idx.TargetArch != runtime.GOARCH {
		return "", fmt.Errorf("original executable %q target arch %q does not match host %q", physicalOrigExe, idx.TargetArch, runtime.GOARCH)
	}

	entry, ok := idx.FindVariant(variant)
	if !ok || entry == nil {
		return "", fmt.Errorf("variant %q not found in original executable %q", variant, physicalOrigExe)
	}
	if entry.UncompressedSize != actualSize {
		return "", fmt.Errorf("variant %s size in index (%d) does not match running payload (%d)", variant, entry.UncompressedSize, actualSize)
	}
	if !strings.EqualFold(entry.SHA256, actualDigest) {
		return "", fmt.Errorf("variant %s digest in index (%s) does not match running payload (%s)", variant, entry.SHA256, actualDigest)
	}

	return filepath.Dir(physicalOrigExe), nil
}
