// Package format defines the microfat binary trailer, index structure, integrity verification,
// diagnostics classification, and serialization logic.
package format

import (
	"errors"
	"strings"
	"syscall"
)

// Standard launcher execution stage names.
const (
	StageMemfdCreate     = "memfd_create"
	StageMemfdExtract    = "extract_memfd"
	StageMemfdExec       = "execve_memfd"
	StageCacheDirInit    = "cache_dir_init"
	StageCacheCreateTemp = "cache_create_temp"
	StageCacheExtract    = "cache_extract"
	StageCacheExec       = "execve_cache"
	StageCodecLookup     = "codec_lookup"
	StageDecompress      = "decompress"
	StageLauncherMain    = "launcher_main"
)

// Standard diagnostic remediation hints for operator troubleshooting.
const (
	HintMemfdSeccomp = "memfd_create was blocked by host seccomp/security profile. " +
		"Fallback to cache will be used. Set MICROFAT_EXEC_MODE=cache to skip memfd probe."
	HintMemfdKernelUnsupported = "memfd_create is unsupported on this Linux kernel (< 3.17). " +
		"Set MICROFAT_EXEC_MODE=cache."
	HintFileDescriptorLimit = "File descriptor limit reached. " +
		"Increase nofile limits (ulimit -n) or free open file descriptors."
	HintReadOnlyFS = "Read-only filesystem detected. " +
		"Set $MICROFAT_CACHE_DIR or $TMPDIR to a writable tmpfs/volume."
	HintCacheUnwritable = "Unable to write cached binary. " +
		"Ensure $XDG_CACHE_HOME or $TMPDIR is writable, or mount a writable tmpfs."
	HintDiskFull = "No space left on device. " +
		"Free disk space in cache directory or set $MICROFAT_CACHE_DIR to a volume with available space."
	HintExecNoExec = "execve failed with EACCES. " +
		"Ensure the backing filesystem or tmpfs is not mounted with 'noexec'."
	HintTextBusy = "Binary is currently open for writing by another process (ETXTBSY). " +
		"Retrying or prewarming with --microfat:prewarm avoids concurrency contention."
	HintExecFormat = "Binary format is invalid or incompatible with host kernel/architecture."
	HintUnsupportedCodec = "Binary variant uses an unsupported compression algorithm. " +
		"Update microfat launcher stub or re-package with a supported codec (e.g. zstd, lz4, none)."
	HintDecompressFailed = "Payload decompression failed or corrupted. " +
		"Re-package the binary with 'microfat pack' or verify integrity with 'microfat verify'."
)

// DiagnoseError inspects an execution failure and stage context, returning an actionable
// human-readable remediation hint if a recognized restriction or error is detected.
func DiagnoseError(stage string, err error) string {
	if err == nil {
		return ""
	}

	switch stage {
	case StageMemfdCreate:
		return diagnoseMemfdCreate(err)
	case StageMemfdExec:
		return diagnoseExec(err)
	case StageCacheDirInit, StageCacheCreateTemp, StageCacheExtract:
		return diagnoseCacheWrite(err)
	case StageCacheExec:
		return diagnoseExec(err)
	case StageLauncherMain:
		return diagnoseGeneric(err)
	default:
		return diagnoseGeneric(err)
	}
}

func diagnoseMemfdCreate(err error) string {
	switch {
	case errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES):
		return HintMemfdSeccomp
	case errors.Is(err, syscall.ENOSYS):
		return HintMemfdKernelUnsupported
	case errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE):
		return HintFileDescriptorLimit
	default:
		return ""
	}
}

func diagnoseCacheWrite(err error) string {
	switch {
	case errors.Is(err, syscall.EROFS):
		return HintReadOnlyFS
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return HintCacheUnwritable
	case errors.Is(err, syscall.ENOSPC):
		return HintDiskFull
	case errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE):
		return HintFileDescriptorLimit
	default:
		return ""
	}
}

func diagnoseExec(err error) string {
	switch {
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return HintExecNoExec
	case errors.Is(err, syscall.ETXTBSY):
		return HintTextBusy
	case errors.Is(err, syscall.ENOEXEC):
		return HintExecFormat
	default:
		return ""
	}
}

func diagnoseGeneric(err error) string {
	switch {
	case errors.Is(err, syscall.EROFS):
		return HintReadOnlyFS
	case errors.Is(err, syscall.ENOSPC):
		return HintDiskFull
	case errors.Is(err, syscall.ENOSYS):
		return HintMemfdKernelUnsupported
	case errors.Is(err, syscall.ETXTBSY):
		return HintTextBusy
	case errors.Is(err, syscall.ENOEXEC):
		return HintExecFormat
	case errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE):
		return HintFileDescriptorLimit
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		errStr := err.Error()
		if strings.Contains(errStr, "execve") || strings.Contains(errStr, "exec") {
			return HintExecNoExec
		}
		if strings.Contains(errStr, "memfd") {
			return HintMemfdSeccomp
		}
		return HintCacheUnwritable
	default:
		errStr := err.Error()
		if strings.Contains(errStr, "unsupported compression") {
			return HintUnsupportedCodec
		}
		if strings.Contains(errStr, "decompression failed") || strings.Contains(errStr, "decompressing") {
			return HintDecompressFailed
		}
		return ""
	}
}
