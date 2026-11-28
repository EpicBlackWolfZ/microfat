package cache

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckCacheMetadataFields(t *testing.T) {
	t.Parallel()

	const uid = 1000
	for _, tc := range []struct {
		name        string
		uid         uint32
		unsafeMode  bool
		expectedUID int
		wantErr     error
	}{
		{"safe_mode", uid, false, uid, nil},
		{"negative_euid", uid, false, -1, ErrUnsafeFile},
		{"foreign_uid", uid + 1, false, uid, ErrUnsafeFile},
		{"unsafe_bits_set", uid, true, uid, ErrUnsafeFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkCacheMetadataFields(tc.uid, tc.unsafeMode, tc.expectedUID)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestCheckCacheEntryFields(t *testing.T) {
	t.Parallel()

	const (
		uid          = 1000
		expectedSize = int64(4096)
	)
	for _, tc := range []struct {
		name                   string
		isReg, unsafe, execOk  bool
		size                   int64
		requireExec, checkSize bool
		wantErr                error
	}{
		{"valid_exec_and_size", true, false, true, expectedSize, true, true, nil},
		{"valid_noexec_when_not_required", true, false, false, 0, false, false, nil},
		{"non_regular", false, false, true, expectedSize, true, true, ErrNonRegularFile},
		{"unsafe_mode", true, true, true, expectedSize, true, true, ErrUnsafeFile},
		{"missing_exec_bit", true, false, false, expectedSize, true, true, ErrNotExecutable},
		{"size_mismatch", true, false, true, expectedSize - 1, true, true, ErrSizeMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkCacheEntryFields(uid, tc.isReg, tc.unsafe, tc.execOk, tc.size, uid, expectedSize, tc.requireExec, tc.checkSize)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}
