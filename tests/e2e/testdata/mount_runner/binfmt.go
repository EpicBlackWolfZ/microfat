package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"golang.org/x/sys/unix"
)

const (
	binfmtDirMode  = 0o700
	binfmtFileMode = 0o600
)

func checkBinfmtNamespace(req mountfixture.Request, result *mountfixture.Result) error {
	if req.Binfmt == nil {
		return nil
	}
	ns, err := os.Readlink("/proc/self/ns/user")
	if err != nil {
		return err
	}
	if ns == req.Binfmt.ParentUserNamespace {
		return errors.New("refusing binfmt in the parent user namespace")
	}
	result.UserNamespace = ns
	return nil
}

func setupBinfmt(req mountfixture.Request, result *mountfixture.Result) error {
	if req.Binfmt == nil {
		return nil
	}
	// This mount is created only after checking distinct user/mount/PID namespaces.
	// Never write to the inherited /proc/sys/fs/binfmt_misc mount.
	dir := filepath.Join(req.Root, "binfmt")
	if err := os.MkdirAll(dir, binfmtDirMode); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return errors.New("invalid private binfmt directory")
	}
	if err := unix.Mount("binfmt_misc", dir, "binfmt_misc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, ""); err != nil {
		if req.Binfmt.Preflight && (errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) || errors.Is(err, unix.ENODEV)) {
			result.Prerequisite = "private user-namespace binfmt mount unavailable"
		}
		return fmt.Errorf("private binfmt mount: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "register" && entry.Name() != "status" {
			return errors.New("private binfmt instance contains inherited handlers")
		}
	}
	// binfmt pseudo-inodes can initially use unmapped root IDs. Claim only the
	// register inode of this newly created, empty instance for our mapped UID.
	// #nosec G703 -- path is fixed beneath the validated private binfmt mount.
	if err := os.Chown(filepath.Join(dir, "register"), req.UID, req.GID); err != nil {
		return err
	}
	interpreter := filepath.Join(req.Root, "rootfs", "emulator")
	const magic = `\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\xb7\x00`
	const mask = `\xff\xff\xff\xff\xff\xff\xff\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff`
	registration := ":microfat-qemu-arm64:M::" + magic + ":" + mask + ":" + interpreter + ":F"
	// #nosec G703 -- fixed test registration in the freshly mounted private instance.
	if err := os.WriteFile(filepath.Join(dir, "register"), []byte(registration), binfmtFileMode); err != nil {
		return err
	}
	// #nosec G304 G703 -- read back the fixed private test handler.
	data, err := os.ReadFile(filepath.Join(dir, "microfat-qemu-arm64"))
	result.Registration = string(data)
	return err
}
