//go:build linux

package benchmarkcleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"golang.org/x/sys/unix"
)

const (
	cgroupDirectory  = "/sys/fs/cgroup"
	receiptDirectory = "/run/microfat-benchmark-delegations"
	receiptMode      = 0o600
	registryMode     = 0o700
	maxControlBytes  = 128 * 1024
	maxGroups        = 1024
)

var rootName = regexp.MustCompile(`^microfat-ci-([1-9][0-9]*)-([1-9][0-9]*)-([1-9][0-9]*)$`)
var leafName = regexp.MustCompile(`^microfat-bench-[0-9]+$`)

type receipt struct {
	Root, RunID, Attempt string
	UID                  uint32
	Device, Inode        uint64
}

type environment struct {
	cgroups, receipts string
	privilegedUID     uint32
	statfs            func(int) (int64, error)
	selfCgroup        func() ([]byte, error)
	openPID           func(int) (int, error)
	signalPID         func(int) error
	unlinkAt          func(int, string, int) error
}

func nativeEnvironment() environment {
	return environment{cgroups: cgroupDirectory, receipts: receiptDirectory,
		statfs: func(fd int) (int64, error) {
			var stat unix.Statfs_t
			err := unix.Fstatfs(fd, &stat)
			return stat.Type, err
		}, selfCgroup: func() ([]byte, error) { return os.ReadFile("/proc/self/cgroup") },
		openPID:   func(pid int) (int, error) { return unix.PidfdOpen(pid, 0) },
		signalPID: func(fd int) error { return unix.PidfdSendSignal(fd, unix.SIGTERM, nil, 0) }, unlinkAt: unix.Unlinkat}
}

// Register records the freshly created delegation before it is exported to workflow commands.
func Register(root, runID, attempt string, uid uint32) error {
	if os.Geteuid() != 0 {
		return errors.New("delegation registration requires root")
	}
	return nativeEnvironment().register(root, runID, attempt, uid)
}

// Cleanup must run outside the delegation. Its receipt survives every failed drain or removal.
func Cleanup(ctx context.Context, root, runID, attempt string) error {
	if os.Geteuid() != 0 {
		return errors.New("delegation cleanup requires root")
	}
	return nativeEnvironment().cleanup(ctx, root, runID, attempt)
}

func (e environment) cleanup(ctx context.Context, root, runID, attempt string) error {
	tree, err := e.openContext(ctx, root, runID, attempt)
	if err != nil {
		return err
	}
	defer tree.close()
	return drain(ctx, tree, gracePeriod, forcePeriod)
}

func (e environment) name(root, runID, attempt string) (string, error) {
	name := filepath.Base(root)
	parts := rootName.FindStringSubmatch(name)
	if filepath.Clean(root) != root || filepath.Dir(root) != e.cgroups ||
		len(parts) != 4 || parts[1] != runID || parts[2] != attempt {
		return "", errors.New("delegation path differs from current run and attempt")
	}
	return name, nil
}

func openDirectory(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openAt(parent *os.File, name string, flags int, mode uint32) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func statFile(file *os.File) (unix.Stat_t, error) {
	var stat unix.Stat_t
	err := unix.Fstat(int(file.Fd()), &stat)
	return stat, err
}

func readAt(parent *os.File, name string) ([]byte, error) {
	file, err := openAt(parent, name, unix.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxControlBytes+1))
	if len(data) > maxControlBytes {
		return nil, errors.New("oversized delegation control")
	}
	return data, err
}

func (e environment) registry(create bool) (*os.File, error) {
	if create {
		if err := os.Mkdir(e.receipts, registryMode); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	file, err := openDirectory(e.receipts)
	if err != nil {
		return nil, err
	}
	stat, err := statFile(file)
	if err != nil || stat.Uid != e.privilegedUID || stat.Mode&0o777 != registryMode {
		_ = file.Close()
		return nil, errors.Join(errors.New("unprotected delegation registry"), err)
	}
	return file, nil
}

func (e environment) root(parent *os.File, name string, uid uint32) (*os.File, unix.Stat_t, error) {
	file, err := openAt(parent, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, unix.Stat_t{}, err
	}
	stat, statErr := statFile(file)
	fs, fsErr := e.statfs(int(file.Fd()))
	if statErr != nil || fsErr != nil || fs != unix.CGROUP2_SUPER_MAGIC || stat.Uid != uid || stat.Mode&0o022 != 0 {
		_ = file.Close()
		return nil, stat, errors.Join(errors.New("unowned or non-v2 delegation"), statErr, fsErr)
	}
	data, err := readAt(file, "cgroup.type")
	if err != nil || string(data) != "domain\n" {
		_ = file.Close()
		return nil, stat, errors.Join(errors.New("delegation is not a domain cgroup"), err)
	}
	return file, stat, nil
}

func (e environment) register(root, runID, attempt string, uid uint32) error {
	name, err := e.name(root, runID, attempt)
	if err != nil {
		return err
	}
	parent, err := openDirectory(e.cgroups)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	group, stat, err := e.root(parent, name, uid)
	if err != nil {
		return err
	}
	defer func() { _ = group.Close() }()
	registry, err := e.registry(true)
	if err != nil {
		return err
	}
	defer func() { _ = registry.Close() }()
	file, err := openAt(registry, name+".json", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, receiptMode)
	if err != nil {
		return err
	}
	value := receipt{root, runID, attempt, uid, stat.Dev, stat.Ino}
	writeErr := json.NewEncoder(file).Encode(value)
	return errors.Join(writeErr, file.Close())
}

func parseUID(text string) (uint32, error) {
	uid, err := strconv.ParseUint(text, 10, 32)
	return uint32(uid), err
}

// Run is the prebuilt helper's argument contract. No build or download occurs during teardown.
func Run(ctx context.Context, args []string) error {
	if len(args) == 5 && args[0] == "register" {
		uid, err := parseUID(args[4])
		if err != nil {
			return err
		}
		return Register(args[1], args[2], args[3], uid)
	}
	if len(args) == 4 && args[0] == "cleanup" {
		return Cleanup(ctx, args[1], args[2], args[3])
	}
	return fmt.Errorf("usage: benchmark-cleanup register ROOT RUN ATTEMPT UID | cleanup ROOT RUN ATTEMPT")
}
