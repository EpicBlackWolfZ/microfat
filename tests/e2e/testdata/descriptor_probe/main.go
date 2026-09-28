// Descriptor probe deliberately retains an execution descriptor only in its
// diagnostic keep control. It is never part of a shipped launcher.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"syscall"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"golang.org/x/sys/unix"
)

const (
	argumentCount  = 4
	executableMode = 0o700
	payloadLimit   = 32 << 20
	allSeals       = unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func openPayload(path, storage string) (*os.File, error) {
	// #nosec G304 G703 -- fixture-selected payload within a disposable chroot.
	source, err := os.Open(path)
	if err != nil || storage == "disk" {
		return source, err
	}
	defer func() { _ = source.Close() }()
	if storage != "memfd" {
		return nil, errors.New("storage must be disk or memfd")
	}
	flags := unix.MFD_CLOEXEC | unix.MFD_ALLOW_SEALING
	fd, err := unix.MemfdCreate("qemu-probe", flags|unix.MFD_EXEC)
	if errors.Is(err, unix.EINVAL) {
		fd, err = unix.MemfdCreate("qemu-probe", flags)
	}
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "qemu-probe")
	n, copyErr := io.Copy(file, io.LimitReader(source, payloadLimit+1))
	if copyErr != nil || n > payloadLimit {
		return nil, errors.Join(errors.New("cannot copy bounded payload"), copyErr, file.Close())
	}
	if err := unix.Fchmod(fd, executableMode); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if _, err := unix.FcntlInt(file.Fd(), unix.F_ADD_SEALS, allSeals); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func emit(record mountfixture.DescriptorProbe) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stderr, "[qemu-probe] %s\n", data)
	return err
}

func run() error {
	if len(os.Args) < argumentCount || (os.Args[3] != "keep" && os.Args[3] != "cloexec") {
		return errors.New("usage: descriptor-probe payload disk|memfd keep|cloexec [payload args...]")
	}
	file, err := openPayload(os.Args[1], os.Args[2])
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(file, 0, payloadLimit)); err != nil {
		return err
	}
	flags := unix.FD_CLOEXEC
	if os.Args[3] == "keep" {
		flags = 0
	}
	if _, err := unix.FcntlInt(file.Fd(), unix.F_SETFD, flags); err != nil {
		return err
	}
	actual, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil {
		return err
	}
	record := mountfixture.DescriptorProbe{Event: "before-exec", PID: os.Getpid(), FD: int(file.Fd()),
		Flags: actual, Storage: os.Args[2], Digest: fmt.Sprintf("%x", hash.Sum(nil))}
	record.Target, err = os.Readlink("/proc/self/fd/" + strconv.Itoa(record.FD))
	if err != nil {
		return err
	}
	if os.Args[2] == "memfd" {
		record.Seals, err = unix.FcntlInt(file.Fd(), unix.F_GET_SEALS, 0)
		if err != nil {
			return err
		}
	}
	if err := emit(record); err != nil {
		return err
	}
	path := "/proc/self/fd/" + strconv.Itoa(record.FD)
	env := append(os.Environ(), "QEMU_PROBE_FD="+strconv.Itoa(record.FD))
	// #nosec G204 G702 -- intentional diagnostic exec of the verified fixture descriptor.
	err = syscall.Exec(path, append([]string{os.Args[1]}, os.Args[argumentCount:]...), env)
	record.Event, record.Error = "exec-returned", fmt.Sprint(err)
	return errors.Join(err, emit(record))
}
