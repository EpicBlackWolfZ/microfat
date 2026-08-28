package format

import (
	"errors"
	"fmt"
	"syscall"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
)

func TestDiagnoseError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		stage        string
		err          error
		expectedHint string
	}{
		{
			name:         "nil error returns empty hint",
			stage:        StageMemfdCreate,
			err:          nil,
			expectedHint: "",
		},
		{
			name:         "memfd_create EPERM seccomp blocked",
			stage:        StageMemfdCreate,
			err:          syscall.EPERM,
			expectedHint: HintMemfdSeccomp,
		},
		{
			name:         "memfd_create EACCES wrapped",
			stage:        StageMemfdCreate,
			err:          fmt.Errorf("memfd probe failed: %w", syscall.EACCES),
			expectedHint: HintMemfdSeccomp,
		},
		{
			name:         "memfd_create ENOSYS unsupported kernel",
			stage:        StageMemfdCreate,
			err:          syscall.ENOSYS,
			expectedHint: HintMemfdKernelUnsupported,
		},
		{
			name:         "memfd_create EMFILE fd limit",
			stage:        StageMemfdCreate,
			err:          syscall.EMFILE,
			expectedHint: HintFileDescriptorLimit,
		},
		{
			name:         "memfd_create ENFILE system fd limit",
			stage:        StageMemfdCreate,
			err:          syscall.ENFILE,
			expectedHint: HintFileDescriptorLimit,
		},
		{
			name:         "memfd_create unmapped error returns empty",
			stage:        StageMemfdCreate,
			err:          errors.New("unknown random error"),
			expectedHint: "",
		},
		{
			name:         "cache dir init EROFS read only fs",
			stage:        StageCacheDirInit,
			err:          syscall.EROFS,
			expectedHint: HintReadOnlyFS,
		},
		{
			name:         "cache create temp EACCES unwritable",
			stage:        StageCacheCreateTemp,
			err:          syscall.EACCES,
			expectedHint: HintCacheUnwritable,
		},
		{
			name:         "cache extract EPERM unwritable",
			stage:        StageCacheExtract,
			err:          syscall.EPERM,
			expectedHint: HintCacheUnwritable,
		},
		{
			name:         "cache extract ENOSPC disk full",
			stage:        StageCacheExtract,
			err:          syscall.ENOSPC,
			expectedHint: HintDiskFull,
		},
		{
			name:         "cache extract EMFILE fd exhaustion",
			stage:        StageCacheExtract,
			err:          syscall.EMFILE,
			expectedHint: HintFileDescriptorLimit,
		},
		{
			name:         "cache extract unmapped error returns empty",
			stage:        StageCacheExtract,
			err:          errors.New("unmapped"),
			expectedHint: "",
		},
		{
			name:         "memfd execve EACCES noexec mount",
			stage:        StageMemfdExec,
			err:          syscall.EACCES,
			expectedHint: HintExecNoExec,
		},
		{
			name:         "memfd execve EPERM",
			stage:        StageMemfdExec,
			err:          syscall.EPERM,
			expectedHint: HintExecNoExec,
		},
		{
			name:         "cache execve ETXTBSY text file busy",
			stage:        StageCacheExec,
			err:          syscall.ETXTBSY,
			expectedHint: HintTextBusy,
		},
		{
			name:         "cache execve ENOEXEC invalid format",
			stage:        StageCacheExec,
			err:          syscall.ENOEXEC,
			expectedHint: HintExecFormat,
		},
		{
			name:         "cache execve unmapped error returns empty",
			stage:        StageCacheExec,
			err:          errors.New("unmapped"),
			expectedHint: "",
		},
		{
			name:         "codec lookup with ErrUnsupportedCodec",
			stage:        StageCodecLookup,
			err:          fmt.Errorf("%w: foo", codec.ErrUnsupportedCodec),
			expectedHint: HintUnsupportedCodec,
		},
		{
			name:         "decompress with ErrDecompressionFailed",
			stage:        StageDecompress,
			err:          fmt.Errorf("%w: corrupted frame", codec.ErrDecompressionFailed),
			expectedHint: HintDecompressFailed,
		},
		{
			name:         "decompress with ErrSizeMismatch",
			stage:        StageDecompress,
			err:          fmt.Errorf("%w: expected 100, got 50", codec.ErrSizeMismatch),
			expectedHint: HintDecompressFailed,
		},
		{
			name:         "decompress with ErrDictionaryCorrupted",
			stage:        StageDecompress,
			err:          fmt.Errorf("%w: mismatch", ErrDictionaryCorrupted),
			expectedHint: HintDecompressFailed,
		},
		{
			name:         "generic launcher_main EROFS",
			stage:        StageLauncherMain,
			err:          syscall.EROFS,
			expectedHint: HintReadOnlyFS,
		},
		{
			name:         "generic launcher_main ENOSPC",
			stage:        StageLauncherMain,
			err:          syscall.ENOSPC,
			expectedHint: HintDiskFull,
		},
		{
			name:         "generic launcher_main ENOSYS",
			stage:        StageLauncherMain,
			err:          syscall.ENOSYS,
			expectedHint: HintMemfdKernelUnsupported,
		},
		{
			name:         "generic launcher_main ETXTBSY",
			stage:        StageLauncherMain,
			err:          syscall.ETXTBSY,
			expectedHint: HintTextBusy,
		},
		{
			name:         "generic launcher_main ENOEXEC",
			stage:        StageLauncherMain,
			err:          syscall.ENOEXEC,
			expectedHint: HintExecFormat,
		},
		{
			name:         "generic launcher_main EMFILE",
			stage:        StageLauncherMain,
			err:          syscall.EMFILE,
			expectedHint: HintFileDescriptorLimit,
		},
		{
			name:         "generic launcher_main ENFILE",
			stage:        StageLauncherMain,
			err:          syscall.ENFILE,
			expectedHint: HintFileDescriptorLimit,
		},
		{
			name:         "generic launcher_main ErrExecve with EACCES",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w on cached binary: %w", ErrExecve, syscall.EACCES),
			expectedHint: HintExecNoExec,
		},
		{
			name:         "generic launcher_main ErrExecve with EPERM",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w: %w", ErrExecve, syscall.EPERM),
			expectedHint: HintExecNoExec,
		},
		{
			name:         "generic launcher_main ErrExecve with ETXTBSY",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w: %w", ErrExecve, syscall.ETXTBSY),
			expectedHint: HintTextBusy,
		},
		{
			name:         "generic launcher_main ErrExecve with ENOEXEC",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w: %w", ErrExecve, syscall.ENOEXEC),
			expectedHint: HintExecFormat,
		},
		{
			name:         "generic launcher_main ErrMemfdCreate with EPERM",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w: %w", ErrMemfdCreate, syscall.EPERM),
			expectedHint: HintMemfdSeccomp,
		},
		{
			name:         "generic launcher_main ErrMemfdCreate with EACCES",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w: %w", ErrMemfdCreate, syscall.EACCES),
			expectedHint: HintMemfdSeccomp,
		},
		{
			name:         "generic launcher_main ErrMemfdCreate with ENOSYS",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w: %w", ErrMemfdCreate, syscall.ENOSYS),
			expectedHint: HintMemfdKernelUnsupported,
		},
		{
			name:         "generic launcher_main ErrMemfdCreate with EMFILE",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w: %w", ErrMemfdCreate, syscall.EMFILE),
			expectedHint: HintFileDescriptorLimit,
		},
		{
			name:         "generic launcher_main ErrMemfdCreate with ENFILE",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w: %w", ErrMemfdCreate, syscall.ENFILE),
			expectedHint: HintFileDescriptorLimit,
		},
		{
			name:         "generic launcher_main ErrCacheInit with EROFS",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w: %w", ErrCacheInit, syscall.EROFS),
			expectedHint: HintReadOnlyFS,
		},
		{
			name:         "generic launcher_main ErrCacheWrite with EACCES",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w: %w", ErrCacheWrite, syscall.EACCES),
			expectedHint: HintCacheUnwritable,
		},
		{
			name:         "generic launcher_main ErrCacheExtract with ENOSPC",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w: %w", ErrCacheExtract, syscall.ENOSPC),
			expectedHint: HintDiskFull,
		},
		{
			name:         "generic launcher_main ErrCacheExtract with EMFILE",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("%w: %w", ErrCacheExtract, syscall.EMFILE),
			expectedHint: HintFileDescriptorLimit,
		},
		{
			name:         "generic launcher_main fallback EACCES without sentinel",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("creating directory: %w", syscall.EACCES),
			expectedHint: HintCacheUnwritable,
		},
		{
			name:         "generic launcher_main unmapped error",
			stage:        StageLauncherMain,
			err:          errors.New("unmapped general error"),
			expectedHint: "",
		},
		{
			name:         "generic launcher_main unsupported compression",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("resolving codec: %w", codec.ErrUnsupportedCodec),
			expectedHint: HintUnsupportedCodec,
		},
		{
			name:         "generic launcher_main decompression failed",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("reading variant: %w", codec.ErrDecompressionFailed),
			expectedHint: HintDecompressFailed,
		},
		{
			name:         "generic launcher_main size mismatch",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("decompressing variant: %w", codec.ErrSizeMismatch),
			expectedHint: HintDecompressFailed,
		},
		{
			name:         "generic launcher_main dictionary corrupted",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("reading dict: %w", ErrDictionaryCorrupted),
			expectedHint: HintDecompressFailed,
		},
		{
			name:         "unknown stage uses generic",
			stage:        "custom_stage",
			err:          syscall.EROFS,
			expectedHint: HintReadOnlyFS,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			actualHint := DiagnoseError(tc.stage, tc.err)
			if actualHint != tc.expectedHint {
				t.Errorf("DiagnoseError(%q, %v) = %q, expected %q", tc.stage, tc.err, actualHint, tc.expectedHint)
			}
		})
	}
}
