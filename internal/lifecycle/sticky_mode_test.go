package lifecycle_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/lifecycle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const specialModeMask = os.ModeSetuid | os.ModeSetgid | os.ModeSticky

func TestExecute_StickyModePolicies(t *testing.T) {
	t.Parallel()

	for _, policy := range []lifecycle.MetadataPolicy{lifecycle.PolicyStrict, lifecycle.PolicyStrip} {
		for _, intent := range []lifecycle.PublicationIntent{lifecycle.IntentCreateOnly, lifecycle.IntentReplaceSource} {
			t.Run(string(policy)+"/"+string(intent), func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				src := createTestBinary(t, dir, "source", []byte("original"))
				sourceMode := os.FileMode(testFilePerm) | os.ModeSticky
				require.NoError(t, os.Chmod(src, sourceMode))
				sourceInfo, err := os.Stat(src)
				require.NoError(t, err)
				require.Equal(t, sourceMode, sourceInfo.Mode()&(os.ModePerm|specialModeMask))
				dest := filepath.Join(dir, "derivative")
				if intent == lifecycle.IntentReplaceSource {
					dest = src
				}
				err = lifecycle.Execute(lifecycle.Transaction{
					SrcPath: src, DestPath: dest, Intent: intent,
					Opts: lifecycle.Options{Policy: policy},
					Transform: func(staged *os.File) error {
						_, err := staged.Write([]byte("transformed"))
						return err
					},
				})
				require.NoError(t, err)
				info, err := os.Stat(dest)
				require.NoError(t, err)
				expectedMode := sourceMode
				if policy == lifecycle.PolicyStrip {
					expectedMode = testFilePerm
				}
				assert.Equal(t, expectedMode, info.Mode()&(os.ModePerm|specialModeMask))
				if intent == lifecycle.IntentCreateOnly {
					sourceInfo, err = os.Stat(src)
					require.NoError(t, err)
					assert.Equal(t, sourceMode, sourceInfo.Mode()&(os.ModePerm|specialModeMask))
				}
			})
		}
	}
}

func TestExecute_ReadbackRejectsSpecialModeMismatch(t *testing.T) {
	// Chmod fault injection mutates a package hook, so these subtests run serially.
	tests := []struct {
		name       string
		policy     lifecycle.MetadataPolicy
		sourceMode os.FileMode
		actualMode os.FileMode
	}{
		{name: "strict_sticky_dropped", policy: lifecycle.PolicyStrict,
			sourceMode: testFilePerm | os.ModeSticky, actualMode: testFilePerm},
		{name: "strict_sticky_added", policy: lifecycle.PolicyStrict,
			sourceMode: testFilePerm, actualMode: testFilePerm | os.ModeSticky},
		{name: "strict_setuid_added", policy: lifecycle.PolicyStrict,
			sourceMode: testFilePerm, actualMode: testFilePerm | os.ModeSetuid},
		{name: "strict_setgid_added", policy: lifecycle.PolicyStrict,
			sourceMode: testFilePerm, actualMode: testFilePerm | os.ModeSetgid},
		{name: "strip_sticky_retained", policy: lifecycle.PolicyStrip,
			sourceMode: testFilePerm | os.ModeSticky, actualMode: testFilePerm | os.ModeSticky},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := createTestBinary(t, dir, "source", []byte("original"))
			require.NoError(t, os.Chmod(src, tc.sourceMode))
			calls := 0
			restore := lifecycle.SetChmodFuncForTest(func(staged *os.File, mode os.FileMode) error {
				calls++
				if calls > 1 {
					mode = tc.actualMode
				}
				return staged.Chmod(mode)
			})
			t.Cleanup(restore)
			dest := filepath.Join(dir, "derivative")
			err := lifecycle.Execute(lifecycle.Transaction{
				SrcPath: src, DestPath: dest, Intent: lifecycle.IntentCreateOnly,
				Opts: lifecycle.Options{Policy: tc.policy},
				Transform: func(staged *os.File) error {
					_, err := staged.Write([]byte("transformed"))
					return err
				},
			})
			require.ErrorIs(t, err, lifecycle.ErrReadbackVerification)
			assert.NoFileExists(t, dest)
			contents, err := os.ReadFile(src)
			require.NoError(t, err)
			assert.Equal(t, "original", string(contents))
			staged, err := filepath.Glob(filepath.Join(dir, ".microfat-tx-*.tmp"))
			require.NoError(t, err)
			assert.Empty(t, staged, "failed readback must remove the unpublished stage")
		})
	}
}
