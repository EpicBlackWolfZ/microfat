package install

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckInstallEntryMetadata(t *testing.T) {
	t.Parallel()

	const uid int64 = 1000

	for _, tc := range []struct {
		name                         string
		entryUID, expectedUID        int64
		isDir, isRegular             bool
		nlink                        uint64
		unsafeWrite, sticky, special bool
		expectDir, exactUID          bool
		wantErr                      error
	}{
		{"valid_exact_dir", uid, uid, true, false, 2, false, false, false, true, true, nil},
		{"valid_root_sticky_ancestor", 0, uid, true, false, 2, true, true, false, true, false, nil},
		{"valid_regular_file", uid, uid, false, true, 1, false, false, false, false, true, nil},
		{"negative_expected_uid", uid, -1, false, true, 1, false, false, false, false, true, ErrOwnership},
		{"foreign_uid_exact", 0, uid, true, false, 2, false, false, false, true, true, ErrOwnership},
		{"foreign_uid_ancestor", uid + 1, uid, true, false, 2, false, false, false, true, false, ErrOwnership},
		{"not_a_dir", uid, uid, false, true, 1, false, false, false, true, true, ErrOwnership},
		{"hard_link_regular", uid, uid, false, true, 2, false, false, false, false, true, ErrOwnership},
		{"group_writable", uid, uid, false, true, 1, true, false, false, false, true, ErrOwnership},
		{"setuid_bit", uid, uid, false, true, 1, false, false, true, false, true, ErrOwnership},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkInstallEntryMetadata(
				tc.entryUID, tc.expectedUID,
				tc.isDir, tc.isRegular,
				tc.nlink,
				tc.unsafeWrite, tc.sticky, tc.special,
				tc.expectDir, tc.exactUID,
			)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestCheckInstallFileSize(t *testing.T) {
	t.Parallel()
	require.False(t, checkInstallFileSize(0))
	require.False(t, checkInstallFileSize(-1))
	require.True(t, checkInstallFileSize(1))
	require.True(t, checkInstallFileSize(MaxFileBytes))
	require.False(t, checkInstallFileSize(MaxFileBytes+1))
}
