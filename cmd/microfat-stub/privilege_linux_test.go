//go:build linux

package main

import (
	"encoding/binary"
	"errors"
	"golang.org/x/sys/unix"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSecureAuxv(t *testing.T) {
	t.Parallel()
	const secure = 23
	for _, tc := range []struct {
		name  string
		words []uint64
		valid bool
	}{
		{"ordinary", []uint64{secure, 0, 0, 0}, true},
		{"elevated equal IDs", []uint64{secure, 1, 0, 0}, false},
		{"missing", []uint64{0, 0}, false},
		{"truncated", []uint64{secure}, false},
		{"unterminated", []uint64{secure, 0}, false},
		{"duplicate", []uint64{secure, 0, secure, 0, 0, 0}, false},
		{"other tags", []uint64{1, 1, secure, 0, 0, 0}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var data []byte
			for _, v := range tc.words {
				data = binary.NativeEndian.AppendUint64(data, v)
			}
			err := validateSecureAuxv(data)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, errElevatedExecution)
			}
		})
	}
}

func TestPrivilegeRejectionPrecedesSideEffects(t *testing.T) {
	original := checkLauncherPrivilegeFunc
	pathProbe := getSelfExecutablePathFunc
	exit := exitFunc
	t.Cleanup(func() { checkLauncherPrivilegeFunc = original; getSelfExecutablePathFunc = pathProbe; exitFunc = exit })
	checkLauncherPrivilegeFunc = func() error { return errElevatedExecution }
	getSelfExecutablePathFunc = func() (string, error) { t.Fatal("executable resolution before rejection"); return "", nil }
	exitFunc = func(code int) { require.Equal(t, 1, code) }
	t.Setenv("MICROFAT_LOG", "json")
	require.ErrorIs(t, run(), errElevatedExecution)
	require.ErrorIs(t, runBinary("must-not-open"), errElevatedExecution)
	main()
}

func TestPrivilegeProbeFailures(t *testing.T) {
	ids, auxv, caps := launcherIDsFunc, readSecureAuxvFunc, executableCapabilityFunc
	t.Cleanup(func() { launcherIDsFunc = ids; readSecureAuxvFunc = auxv; executableCapabilityFunc = caps })
	const secure = 23
	valid := binary.NativeEndian.AppendUint64(nil, secure)
	valid = binary.NativeEndian.AppendUint64(valid, 0)
	valid = append(valid, make([]byte, 16)...)
	for _, tc := range []struct {
		name                 string
		uid, euid, gid, egid int
		data                 []byte
		readErr, capErr      error
		succeeds             bool
	}{
		{"user", 1, 1, 1, 1, valid, nil, unix.ENODATA, true},
		{"root", 0, 0, 0, 0, valid, nil, unix.ENODATA, true},
		{"setuid", 1, 0, 1, 1, valid, nil, unix.ENODATA, false},
		{"setgid", 1, 1, 1, 0, valid, nil, unix.ENODATA, false},
		{"unknown secure state", 1, 1, 1, 1, nil, errors.New("read failed"), unix.ENODATA, false},
		{"bad auxv", 1, 1, 1, 1, nil, nil, unix.ENODATA, false},
		{"capability bearing", 1, 1, 1, 1, valid, nil, nil, false},
		{"capability probe denied", 1, 1, 1, 1, valid, nil, unix.EACCES, false},
		{"filesystem no xattrs", 1, 1, 1, 1, valid, nil, unix.ENOTSUP, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launcherIDsFunc = func() (int, int, int, int) { return tc.uid, tc.euid, tc.gid, tc.egid }
			readSecureAuxvFunc = func() ([]byte, error) { return tc.data, tc.readErr }
			executableCapabilityFunc = func() error { return tc.capErr }
			err := checkLauncherPrivilege()
			if tc.succeeds {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, errElevatedExecution)
			}
		})
	}
}
