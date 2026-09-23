package lifecycle_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/lifecycle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTakeSourceSnapshot_Errors(t *testing.T) {
	// 1. Non-existent path
	_, err := lifecycle.TakeSourceSnapshot(nil, "/non/existent/microfat/app")
	require.Error(t, err)

	// 2. readXattrs returns error
	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app", []byte("bin"))

	restore := lifecycle.SetReadXattrsFuncForTest(func(string) (map[string][]byte, error) {
		return nil, errors.New("simulated xattr read error")
	})
	defer restore()

	_, err = lifecycle.TakeSourceSnapshot(nil, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading extended attributes")
}

func TestExecute_DirAndStagingErrors(t *testing.T) {
	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app", []byte("bin"))

	// 1. MkdirAll fails because parent is an existing regular file
	existingFile := filepath.Join(tmpDir, "file_not_dir")
	require.NoError(t, os.WriteFile(existingFile, []byte("data"), 0o600))
	destUnderFile := filepath.Join(existingFile, "sub", "app.trimmed")

	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: destUnderFile,
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(_ *os.File) error {
			return nil
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating directory")

	// 2. CreateTemp fails because destDir is read-only
	readOnlyDir := filepath.Join(tmpDir, "readonly")
	require.NoError(t, os.Mkdir(readOnlyDir, 0o500))
	destInReadOnly := filepath.Join(readOnlyDir, "dest.bin")

	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: destInReadOnly,
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(_ *os.File) error {
			return nil
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrStagingFailed)

	// 3. chmodFunc fails setting staging permissions
	callCount := 0
	restore := lifecycle.SetChmodFuncForTest(func(f *os.File, mode os.FileMode) error {
		callCount++
		if callCount == 1 {
			return errors.New("simulated chmod 0600 failure")
		}
		return f.Chmod(mode)
	})
	defer restore()

	destNormal := filepath.Join(tmpDir, "dest_chmod_fail.bin")
	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: destNormal,
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(_ *os.File) error {
			return nil
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrStagingFailed)
}

func TestExecute_ApplyMetadataAndReadbackErrors(t *testing.T) {
	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app", []byte("binary_payload"))

	// 1. Strict mode: chown fails when running as non-root with different UID
	restoreChown := lifecycle.SetChownFuncForTest(func(_ *os.File, _, _ int) error {
		return errors.New("chown operation not permitted")
	})
	defer restoreChown()

	// Use an open file or mock snapshot by forcing UID mismatch
	// Note: We can test chownFunc returning error
	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: filepath.Join(tmpDir, "dest_strict_chown.bin"),
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, err := staged.Write([]byte("transformed"))
			return err
		},
	})
	require.NoError(t, err)
	// In default setup running as test user, snap.UID == os.Geteuid(), so chown error is ignored unless UID differs.
	// But let's verify if setFdXattr fails
	restoreChown()

	// 2. Strict mode: setFdXattrFunc fails
	restoreXattr := lifecycle.SetSetFdXattrFuncForTest(func(_ int, _ string, _ []byte) error {
		return errors.New("simulated fsetxattr error")
	})
	defer restoreXattr()

	restoreReadXattr := lifecycle.SetReadXattrsFuncForTest(func(string) (map[string][]byte, error) {
		return map[string][]byte{"user.foo": []byte("bar")}, nil
	})
	defer restoreReadXattr()

	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: filepath.Join(tmpDir, "dest_strict_xattr.bin"),
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, err := staged.Write([]byte("transformed"))
			return err
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "setting extended attribute")
	restoreXattr()
	restoreReadXattr()

	// 3. chmodFunc fails applying target mode
	chmodCalls := 0
	restoreChmod := lifecycle.SetChmodFuncForTest(func(f *os.File, mode os.FileMode) error {
		chmodCalls++
		if chmodCalls == 2 {
			// Second chmod is the target mode
			return errors.New("simulated target chmod failure")
		}
		return f.Chmod(mode)
	})
	defer restoreChmod()

	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: filepath.Join(tmpDir, "dest_target_chmod.bin"),
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, err := staged.Write([]byte("transformed"))
			return err
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chmodding staged file")
	restoreChmod()

	// 4. verifyReadback: mode does not match (chmod was a no-op so staged mode is 0600 != target 0755)
	restoreChmodNoop := lifecycle.SetChmodFuncForTest(func(_ *os.File, _ os.FileMode) error {
		// Do not actually chmod, leaving mode as 0600
		return nil
	})
	defer restoreChmodNoop()

	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: filepath.Join(tmpDir, "dest_readback_mode.bin"),
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, err := staged.Write([]byte("transformed"))
			return err
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrReadbackVerification)
	assert.Contains(t, err.Error(), "does not match target")
	restoreChmodNoop()

	// 5. Durability sync fails
	restoreSyncDir := lifecycle.SetSyncDirFuncForTest(func(string) error {
		return errors.New("simulated sync dir failure")
	})
	defer restoreSyncDir()

	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: filepath.Join(tmpDir, "dest_sync_fail.bin"),
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, err := staged.Write([]byte("transformed"))
			return err
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrDurabilitySyncFailed)
	restoreSyncDir()
}

func TestExecute_StripModeUpgradesNonExecutable(t *testing.T) {
	tmpDir := t.TempDir()
	// Create file with 0644 (no execute bits)
	src := filepath.Join(tmpDir, "non_exec.bin")
	require.NoError(t, os.WriteFile(src, []byte("plain text or data"), 0o644))

	dest := filepath.Join(tmpDir, "exec.bin")
	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: dest,
		Opts: lifecycle.Options{
			Policy: lifecycle.PolicyStrip,
		},
		Transform: func(staged *os.File) error {
			_, err := staged.Write([]byte("upgraded_to_executable"))
			return err
		},
	})
	require.NoError(t, err)

	fi, err := os.Stat(dest)
	require.NoError(t, err)
	// Mode should have been upgraded with standardExecMask (0755)
	assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm())
}

func TestExecute_InPlace_SameDevIno_DifferentPath(t *testing.T) {
	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app.bin", []byte("orig"))
	destLink := filepath.Join(tmpDir, "app_link.bin")
	require.NoError(t, os.Link(src, destLink))

	// destLink has same dev & ino as src, but different string.
	// Passing BreakHardlinks: true should treat it as inPlace and sever the link.
	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: destLink,
		Opts: lifecycle.Options{
			Policy:         lifecycle.PolicyStrict,
			BreakHardlinks: true,
		},
		Transform: func(staged *os.File) error {
			_, err := staged.Write([]byte("new_content"))
			return err
		},
	})
	require.NoError(t, err)

	destData, err := os.ReadFile(destLink)
	require.NoError(t, err)
	assert.Equal(t, []byte("new_content"), destData)
}

func TestExecute_InPlace_TargetRemovedBeforePublish(t *testing.T) {
	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app.bin", []byte("orig"))

	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath: src,
		Opts:    lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, err := staged.Write([]byte("data"))
			// Remove the source file behind the transaction's back!
			_ = os.Remove(src)
			return err
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrSourceModified)
}

func TestTakeSourceSnapshot_FallbackStat(t *testing.T) {
	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app", []byte("bin"))

	restore := lifecycle.SetFileStatMetadataFuncForTest(func(os.FileInfo) (uint64, uint64, uint64, int, int, bool) {
		return 0, 0, 1, 0, 0, false
	})
	defer restore()

	snap, err := lifecycle.TakeSourceSnapshot(nil, src)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), snap.Dev)
	assert.Equal(t, uint64(0), snap.Ino)
	assert.Equal(t, uint64(1), snap.Nlink)
}

func TestExecute_NonExistentSourceAndUnsafeValidation(t *testing.T) {

	// 1. Non-existent source path fails in resolveTargetAndSnapshot
	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath: "/non/existent/microfat/path",
		Opts:    lifecycle.DefaultOptions(),
		Transform: func(_ *os.File) error {
			return nil
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stating source executable")

	// 2. Setuid source binary fails in strict mode
	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "setuid_app", []byte("payload"))
	require.NoError(t, os.Chmod(src, 0o755|os.ModeSetuid))

	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath: src,
		Opts:    lifecycle.DefaultOptions(),
		Transform: func(_ *os.File) error {
			return nil
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrUnsafeSetuidSetgid)
}

func TestExecute_InPlace_Symlink_EvaluatedTargetDifference(t *testing.T) {
	tmpDir := t.TempDir()
	realSrc := createTestBinary(t, tmpDir, "real_app", []byte("real_payload"))
	symlinkPath := filepath.Join(tmpDir, "symlink_app")
	require.NoError(t, os.Symlink(realSrc, symlinkPath))

	otherFile := createTestBinary(t, tmpDir, "other_app", []byte("other"))
	f, err := os.Open(otherFile)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath: symlinkPath,
		SrcFile: f,
		Opts:    lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, err := staged.Write([]byte("updated_real"))
			return err
		},
	})
	require.NoError(t, err)

	updatedReal, err := os.ReadFile(realSrc)
	require.NoError(t, err)
	assert.Equal(t, []byte("updated_real"), updatedReal)
}

func TestExecute_ApplyMetadata_OwnershipError_NonRoot(t *testing.T) {
	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app", []byte("bin"))

	restoreChown := lifecycle.SetChownFuncForTest(func(_ *os.File, _, _ int) error {
		return errors.New("chown EPERM")
	})
	defer restoreChown()

	restoreGeteuid := lifecycle.SetGeteuidFuncForTest(func() int {
		return 99999
	})
	defer restoreGeteuid()

	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: filepath.Join(tmpDir, "dest_strict_chown.bin"),
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, err := staged.Write([]byte("transformed"))
			return err
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrOwnershipPreservation)
}

func TestExecute_VerifyReadback_RootOwnershipMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app", []byte("bin"))

	restoreGeteuid := lifecycle.SetGeteuidFuncForTest(func() int {
		return 0
	})
	defer restoreGeteuid()

	restoreChown := lifecycle.SetChownFuncForTest(func(_ *os.File, _, _ int) error {
		return nil
	})
	defer restoreChown()

	call := 0
	restoreStat := lifecycle.SetFileStatMetadataFuncForTest(func(fi os.FileInfo) (uint64, uint64, uint64, int, int, bool) {
		call++
		if call > 1 {
			// Return different UID for staged file during verifyReadback
			return 1, 1, 1, 9999, 9999, true
		}
		return 1, 1, 1, 1000, 1000, true
	})
	defer restoreStat()

	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: filepath.Join(tmpDir, "dest_root_mismatch.bin"),
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, err := staged.Write([]byte("transformed"))
			return err
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrReadbackVerification)
	assert.Contains(t, err.Error(), "ownership")
}

func TestExecute_PublishInPlace_RenameFails(t *testing.T) {
	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app", []byte("bin"))

	restoreRename := lifecycle.SetRenameFuncForTest(func(string, string) error {
		return errors.New("simulated rename failure")
	})
	defer restoreRename()

	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath: src,
		Opts:    lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, err := staged.Write([]byte("transformed"))
			return err
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "replacing")
}

func TestExecute_StagedSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app", []byte("bin"))

	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: filepath.Join(tmpDir, "dest_closed.bin"),
		Opts: lifecycle.Options{
			Policy: lifecycle.PolicyStrip,
		},
		Transform: func(staged *os.File) error {
			_, _ = staged.Write([]byte("payload"))
			return staged.Close()
		},
	})
	require.Error(t, err)
}
