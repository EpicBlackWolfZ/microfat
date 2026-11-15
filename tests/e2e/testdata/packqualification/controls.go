//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"golang.org/x/sys/unix"
)

func runChild(arguments []string) error {
	if len(arguments) != 3 || arguments[1] != "child" {
		return errors.New("invalid benchmark worker attachment")
	}
	cfg, err := system.ParseChild([]byte(arguments[2]))
	if err != nil {
		return err
	}
	return system.ExecChild(cfg)
}

func verifyWorkerCgroup(input workerInput) error {
	if input.Cgroup == "" {
		return errors.New("worker cgroup identity is missing")
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return err
	}
	relative := strings.TrimPrefix(input.Cgroup, "/sys/fs/cgroup")
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "0::"+relative {
			return nil
		}
	}
	return errors.New("unexpected cgroup membership in Pack worker")
}

func disableSwap(leaf string) error {
	path := filepath.Join(leaf, "memory.swap.max")
	if _, err := os.Stat(path); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte("0"), privateFile); err != nil {
		return err
	}
	actual, err := os.ReadFile(path) // #nosec G304 -- fixed controller file in this attempt's owned cgroup.
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(actual)) != "0" {
		return errors.New("cgroup memory.swap.max readback differs from request")
	}
	return nil
}

func checkDisk(path string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return err
	}
	available := stat.Bavail * uint64(stat.Bsize) // #nosec G115 -- VFS block size is nonnegative.
	if available < minimumDiskBytes {
		return fmt.Errorf("benchmark output has %d free bytes; need %d", available, minimumDiskBytes)
	}
	return nil
}
