//go:build linux

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

var errElevatedExecution = errors.New("privilege-bearing launcher execution is unsupported")

var (
	checkLauncherPrivilegeFunc = checkLauncherPrivilege
	launcherIDsFunc            = func() (int, int, int, int) { return os.Getuid(), os.Geteuid(), os.Getgid(), os.Getegid() }
	readSecureAuxvFunc         = readSecureAuxv
	executableCapabilityFunc   = func() error { _, err := unix.Getxattr("/proc/self/exe", "security.capability", nil); return err }
)

func checkLauncherPrivilege() error {
	uid, euid, gid, egid := launcherIDsFunc()
	if uid != euid || gid != egid {
		return errElevatedExecution
	}
	data, err := readSecureAuxvFunc()
	if err != nil {
		return fmt.Errorf("%w: reading secure-execution state: %v", errElevatedExecution, err)
	}
	if err = validateSecureAuxv(data); err != nil {
		return err
	}
	// AT_SECURE covers actual kernel elevation, including equal-ID capability launches.
	// Reject capability-bearing launcher files even when elevation was suppressed (e.g. root).
	err = executableCapabilityFunc()
	if err == nil {
		return fmt.Errorf("%w: executable carries file capabilities", errElevatedExecution)
	}
	if !errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.ENOTSUP) {
		return fmt.Errorf("%w: inspecting file capabilities: %v", errElevatedExecution, err)
	}
	return nil
}

func readSecureAuxv() ([]byte, error) {
	file, err := os.Open("/proc/self/auxv")
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	const maxAuxvBytes = 4096
	data, err := io.ReadAll(io.LimitReader(file, maxAuxvBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAuxvBytes {
		return nil, errors.New("oversized auxiliary vector")
	}
	return data, nil
}

func validateSecureAuxv(data []byte) error {
	// microfat supports 64-bit Linux ELF launchers on AMD64 and ARM64.
	const wordBytes = 8
	const entryBytes = 2 * wordBytes
	const atSecure = 23
	seen := false
	for len(data) >= entryBytes {
		tag := binary.NativeEndian.Uint64(data[:wordBytes])
		value := binary.NativeEndian.Uint64(data[wordBytes:entryBytes])
		data = data[entryBytes:]
		if tag == 0 {
			if seen {
				return nil
			}
			break
		}
		if tag == atSecure {
			if seen || value != 0 {
				return errElevatedExecution
			}
			seen = true
		}
	}
	return fmt.Errorf("%w: missing or malformed AT_SECURE", errElevatedExecution)
}
