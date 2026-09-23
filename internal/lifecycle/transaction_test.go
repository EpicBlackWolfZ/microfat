package lifecycle_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/lifecycle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	testFilePerm = 0o755
)

func createTestBinary(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, content, testFilePerm))
	return p
}

func TestValidateSnapshot(t *testing.T) {
	t.Parallel()

	require.NoError(t, lifecycle.ValidateSnapshot(nil, lifecycle.PolicyStrict))

	baseSnap := &lifecycle.SourceSnapshot{
		Path: "/tmp/app",
		Mode: testFilePerm,
		UID:  1000,
		GID:  1000,
	}

	t.Run("strict_allows_user_and_selinux_xattrs", func(t *testing.T) {
		t.Parallel()
		snap := *baseSnap
		snap.Xattrs = map[string][]byte{
			"user.tag":         []byte("val"),
			"security.selinux": []byte("unconfined_u:object_r:user_home_t:s0"),
		}
		require.NoError(t, lifecycle.ValidateSnapshot(&snap, lifecycle.PolicyStrict))
	})

	t.Run("strict_rejects_setuid", func(t *testing.T) {
		t.Parallel()
		snap := *baseSnap
		snap.Mode = testFilePerm | os.ModeSetuid
		err := lifecycle.ValidateSnapshot(&snap, lifecycle.PolicyStrict)
		require.Error(t, err)
		require.ErrorIs(t, err, lifecycle.ErrUnsafeSetuidSetgid)
	})

	t.Run("strict_rejects_setgid", func(t *testing.T) {
		t.Parallel()
		snap := *baseSnap
		snap.Mode = testFilePerm | os.ModeSetgid
		err := lifecycle.ValidateSnapshot(&snap, lifecycle.PolicyStrict)
		require.Error(t, err)
		require.ErrorIs(t, err, lifecycle.ErrUnsafeSetuidSetgid)
	})

	t.Run("strict_rejects_capabilities", func(t *testing.T) {
		t.Parallel()
		snap := *baseSnap
		snap.Xattrs = map[string][]byte{
			"security.capability": []byte("cap_data"),
		}
		err := lifecycle.ValidateSnapshot(&snap, lifecycle.PolicyStrict)
		require.Error(t, err)
		require.ErrorIs(t, err, lifecycle.ErrUnsafeCapabilities)
	})

	t.Run("strict_rejects_ima_claim", func(t *testing.T) {
		t.Parallel()
		snap := *baseSnap
		snap.Xattrs = map[string][]byte{
			"security.ima": []byte("hash_data"),
		}
		err := lifecycle.ValidateSnapshot(&snap, lifecycle.PolicyStrict)
		require.Error(t, err)
		require.ErrorIs(t, err, lifecycle.ErrUnsafeIntegrityClaim)
	})

	t.Run("strict_rejects_evm_claim", func(t *testing.T) {
		t.Parallel()
		snap := *baseSnap
		snap.Xattrs = map[string][]byte{
			"security.evm": []byte("evm_data"),
		}
		err := lifecycle.ValidateSnapshot(&snap, lifecycle.PolicyStrict)
		require.Error(t, err)
		require.ErrorIs(t, err, lifecycle.ErrUnsafeIntegrityClaim)
	})

	t.Run("strict_rejects_unapproved_xattr", func(t *testing.T) {
		t.Parallel()
		snap := *baseSnap
		snap.Xattrs = map[string][]byte{
			"trusted.secret": []byte("secret_data"),
		}
		err := lifecycle.ValidateSnapshot(&snap, lifecycle.PolicyStrict)
		require.Error(t, err)
		require.ErrorIs(t, err, lifecycle.ErrUnsupportedXattr)
	})

	t.Run("strip_allows_all_unsafe_attributes", func(t *testing.T) {
		t.Parallel()
		snap := *baseSnap
		snap.Mode = testFilePerm | os.ModeSetuid | os.ModeSetgid
		snap.Xattrs = map[string][]byte{
			"security.capability": []byte("cap_data"),
			"security.ima":        []byte("hash_data"),
			"trusted.secret":      []byte("secret_data"),
		}
		require.NoError(t, lifecycle.ValidateSnapshot(&snap, lifecycle.PolicyStrip))
	})
}

func TestExecute_ValidationErrors(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app", []byte("orig"))

	// Nil transform
	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath: src,
		Intent:  lifecycle.IntentReplaceSource,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transformation callback must not be nil")

	// Empty source
	err = lifecycle.Execute(lifecycle.Transaction{
		Intent:    lifecycle.IntentReplaceSource,
		Transform: func(_ *os.File) error { return nil },
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source path must not be empty")

	// Invalid / empty intent
	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath:   src,
		Transform: func(_ *os.File) error { return nil },
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrInvalidPublicationIntent)

	// Create-only with empty destination
	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath:   src,
		DestPath:  "",
		Intent:    lifecycle.IntentCreateOnly,
		Transform: func(_ *os.File) error { return nil },
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "destination path must not be empty")

	// Invalid policy
	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath: src,
		Intent:  lifecycle.IntentReplaceSource,
		Opts: lifecycle.Options{
			Policy: "invalid",
		},
		Transform: func(_ *os.File) error { return nil },
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrInvalidMetadataPolicy)
}

func TestExecute_FreshDestination_SuccessAndCollision(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	origContent := []byte("original fat binary content")
	src := createTestBinary(t, tmpDir, "app.fat", origContent)
	transformedContent := []byte("transformed single variant content")

	dest := filepath.Join(tmpDir, "sub", "app.trimmed")

	// Fresh destination creates new file
	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: dest,
		Intent:   lifecycle.IntentCreateOnly,
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, writeErr := staged.Write(transformedContent)
			return writeErr
		},
	})
	require.NoError(t, err)

	// Verify original untouched
	srcData, err := os.ReadFile(src)
	require.NoError(t, err)
	assert.Equal(t, origContent, srcData)

	// Verify destination created with transformed content
	destData, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, transformedContent, destData)

	// Second execution to same destination must fail create-only check
	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: dest,
		Intent:   lifecycle.IntentCreateOnly,
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, writeErr := staged.Write([]byte("clobber"))
			return writeErr
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrDestinationExists)

	// Verify destination was NOT clobbered
	destDataAfter, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, transformedContent, destDataAfter)
}

func TestExecute_InPlace_SuccessAndHardlinkProtection(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	origContent := []byte("original fat binary for in-place")
	src := createTestBinary(t, tmpDir, "app.fat", origContent)
	transformedContent := []byte("transformed in-place binary")

	// Normal in-place (1 link) succeeds
	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath: src,
		Intent:  lifecycle.IntentReplaceSource,
		Opts:    lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, writeErr := staged.Write(transformedContent)
			return writeErr
		},
	})
	require.NoError(t, err)

	srcData, err := os.ReadFile(src)
	require.NoError(t, err)
	assert.Equal(t, transformedContent, srcData)

	// Create hard link to test hard link refusal
	hardLinkPath := filepath.Join(tmpDir, "app.link")
	require.NoError(t, os.Link(src, hardLinkPath))

	// In-place without BreakHardlinks must fail
	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath: src,
		Intent:  lifecycle.IntentReplaceSource,
		Opts:    lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, writeErr := staged.Write([]byte("should fail"))
			return writeErr
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrHardLinkDetected)

	// Content must remain untouched
	linkData, err := os.ReadFile(hardLinkPath)
	require.NoError(t, err)
	assert.Equal(t, transformedContent, linkData)

	// In-place WITH BreakHardlinks succeeds and severs the link
	brokenContent := []byte("severed link content")
	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath: src,
		Intent:  lifecycle.IntentReplaceSource,
		Opts: lifecycle.Options{
			Policy:         lifecycle.PolicyStrict,
			BreakHardlinks: true,
		},
		Transform: func(staged *os.File) error {
			_, writeErr := staged.Write(brokenContent)
			return writeErr
		},
	})
	require.NoError(t, err)

	// Verify src has new content, but hardLinkPath retains old content
	newSrcData, err := os.ReadFile(src)
	require.NoError(t, err)
	assert.Equal(t, brokenContent, newSrcData)

	oldLinkData, err := os.ReadFile(hardLinkPath)
	require.NoError(t, err)
	assert.Equal(t, transformedContent, oldLinkData)
}

func TestExecute_InPlace_SymlinkResolution(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	origContent := []byte("target file content")
	target := createTestBinary(t, tmpDir, "real_app", origContent)
	symlinkPath := filepath.Join(tmpDir, "symlink_app")
	require.NoError(t, os.Symlink(target, symlinkPath))

	transformedContent := []byte("transformed real target content")

	// Running in-place on symlink path must resolve and transform the target file
	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath: symlinkPath,
		Intent:  lifecycle.IntentReplaceSource,
		Opts:    lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, writeErr := staged.Write(transformedContent)
			return writeErr
		},
	})
	require.NoError(t, err)

	// Symlink itself must still exist and point to real_app
	linkTarget, err := os.Readlink(symlinkPath)
	require.NoError(t, err)
	assert.Equal(t, target, linkTarget)

	// Real file must have new content
	targetData, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, transformedContent, targetData)
}

func TestExecute_TransformError_CleansUpStaging(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	origContent := []byte("untouched content")
	src := createTestBinary(t, tmpDir, "app.bin", origContent)

	customErr := errors.New("simulated compressor failure")

	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath: src,
		Intent:  lifecycle.IntentReplaceSource,
		Opts:    lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, _ = staged.Write([]byte("partial garbage"))
			return customErr
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, customErr)

	// Original must remain untouched
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	assert.Equal(t, origContent, data)

	// No temporary .tmp files left in directory
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, filepath.Ext(e.Name()) == ".tmp", "temp file %s was not cleaned up", e.Name())
	}
}

func TestExecute_EmptyStaging_FailsReadback(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app.bin", []byte("orig"))

	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath: src,
		Intent:  lifecycle.IntentReplaceSource,
		Opts:    lifecycle.DefaultOptions(),
		Transform: func(_ *os.File) error {
			// Write nothing (0 bytes)
			return nil
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrReadbackVerification)
}

func TestExecute_SourceModifiedDuringTransformation_Aborts(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	origContent := []byte("orig")
	src := createTestBinary(t, tmpDir, "app.bin", origContent)

	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath: src,
		Intent:  lifecycle.IntentReplaceSource,
		Opts:    lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, _ = staged.Write([]byte("new content"))
			// Mutate source file on disk while transform is running!
			time.Sleep(10 * time.Millisecond)
			return os.WriteFile(src, []byte("external modification!"), testFilePerm)
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrSourceModified)
}

func TestExecute_ConcurrentInPlace_SerializedByAdvisoryLock(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app.bin", []byte("content"))

	started := make(chan struct{})
	release := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)

	var err1, err2 error

	go func() {
		defer wg.Done()
		err1 = lifecycle.Execute(lifecycle.Transaction{
			SrcPath: src,
			Intent:  lifecycle.IntentReplaceSource,
			Opts:    lifecycle.DefaultOptions(),
			Transform: func(staged *os.File) error {
				_, _ = staged.Write([]byte("tx1"))
				close(started)
				<-release
				return nil
			},
		})
	}()

	<-started

	// Second concurrent in-place transaction must fail immediately with ErrConcurrentTransformation
	err2 = lifecycle.Execute(lifecycle.Transaction{
		SrcPath: src,
		Intent:  lifecycle.IntentReplaceSource,
		Opts:    lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, _ = staged.Write([]byte("tx2"))
			return nil
		},
	})

	close(release)
	wg.Wait()

	require.NoError(t, err1, "first transaction must succeed")
	require.Error(t, err2, "second concurrent transaction must fail")
	require.ErrorIs(t, err2, lifecycle.ErrConcurrentTransformation)
}

func TestExecute_ExtendedAttributes_PreservedInStrict_StrippedInStrip(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("xattrs tests require Linux")
	}

	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app.bin", []byte("orig"))

	// Set user xattr on source
	attrName := "user.microfat_test"
	attrVal := []byte("sample_xattr_value")
	setErr := unix.Setxattr(src, attrName, attrVal, 0)
	if errors.Is(setErr, unix.ENOTSUP) || errors.Is(setErr, unix.EOPNOTSUPP) {
		t.Skip("filesystem does not support user xattrs")
	}
	require.NoError(t, setErr)

	// 1. Strict mode to new destination must preserve xattr
	destStrict := filepath.Join(tmpDir, "app.strict")
	err := lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: destStrict,
		Intent:   lifecycle.IntentCreateOnly,
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, wErr := staged.Write([]byte("strict"))
			return wErr
		},
	})
	require.NoError(t, err)

	buf := make([]byte, len(attrVal)+10)
	sz, err := unix.Getxattr(destStrict, attrName, buf)
	require.NoError(t, err)
	assert.Equal(t, attrVal, buf[:sz], "strict mode must preserve user xattrs")

	// 2. Strip mode must omit xattr
	destStrip := filepath.Join(tmpDir, "app.strip")
	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		DestPath: destStrip,
		Intent:   lifecycle.IntentCreateOnly,
		Opts: lifecycle.Options{
			Policy: lifecycle.PolicyStrip,
		},
		Transform: func(staged *os.File) error {
			_, wErr := staged.Write([]byte("strip"))
			return wErr
		},
	})
	require.NoError(t, err)

	_, getErr := unix.Getxattr(destStrip, attrName, buf)
	require.Error(t, getErr)
	assert.True(t, errors.Is(getErr, unix.ENODATA))
}

func TestExecute_WithSourceFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	src := createTestBinary(t, tmpDir, "app.bin", []byte("orig"))
	dest := filepath.Join(tmpDir, "dest.bin")

	f, err := os.Open(src)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	err = lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  src,
		SrcFile:  f,
		DestPath: dest,
		Intent:   lifecycle.IntentCreateOnly,
		Opts:     lifecycle.DefaultOptions(),
		Transform: func(staged *os.File) error {
			_, err := io.Copy(staged, f)
			return err
		},
	})
	require.NoError(t, err)
	require.FileExists(t, dest)
}
