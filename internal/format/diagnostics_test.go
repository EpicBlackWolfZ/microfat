package format

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
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
			name:         "generic launcher_main EACCES with execve in message",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("execve failed on cached binary: %w", syscall.EACCES),
			expectedHint: HintExecNoExec,
		},
		{
			name:         "generic launcher_main EPERM with memfd in message",
			stage:        StageLauncherMain,
			err:          fmt.Errorf("memfd_create failed: %w", syscall.EPERM),
			expectedHint: HintMemfdSeccomp,
		},
		{
			name:         "generic launcher_main EACCES fallback",
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
			stage:        StageCodecLookup,
			err:          errors.New("unsupported compression algorithm: foo"),
			expectedHint: HintUnsupportedCodec,
		},
		{
			name:         "generic launcher_main decompression failed",
			stage:        StageDecompress,
			err:          errors.New("decompression failed: corrupted frame"),
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
