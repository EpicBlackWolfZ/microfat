//go:build linux

package system

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func SupportedAffinity(requested []int) ([]int, error) {
	var current unix.CPUSet
	if err := unix.SchedGetaffinity(0, &current); err != nil {
		return nil, err
	}
	var effective []int
	for cpu := range MaxCPU {
		if current.IsSet(cpu) {
			effective = append(effective, cpu)
		}
	}
	for _, cpu := range requested {
		if !current.IsSet(cpu) {
			return effective, fmt.Errorf("CPU %d is outside the allowed affinity", cpu)
		}
	}
	if len(requested) == 0 {
		return effective, nil
	}
	return requested, nil
}

func ExecChild(cfg ChildConfig) error {
	// exec inherits this thread's affinity and destroys the helper's other Go threads.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if len(cfg.Affinity) > 0 {
		var set unix.CPUSet
		for _, cpu := range cfg.Affinity {
			set.Set(cpu)
		}
		if err := unix.SchedSetaffinity(0, &set); err != nil {
			return err
		}
		var effective unix.CPUSet
		if err := unix.SchedGetaffinity(0, &effective); err != nil {
			return err
		}
		if effective != set {
			return fmt.Errorf("effective affinity differs from requested mask")
		}
	}
	for _, file := range cfg.Procs {
		if err := os.WriteFile(file, []byte(strconv.Itoa(os.Getpid())), controlMode); err != nil {
			return err
		}
		data, err := os.ReadFile(file) // #nosec G304 -- validated explicit cgroup.procs attachment.
		if err != nil {
			return err
		}
		if !containsPID(string(data), os.Getpid()) {
			return fmt.Errorf("child membership was not established")
		}
	}
	return unix.Exec(cfg.Path, append([]string{cfg.Path}, cfg.Args...), cfg.Env)
}

func containsPID(data string, pid int) bool {
	for _, value := range strings.Fields(data) {
		if value == strconv.Itoa(pid) {
			return true
		}
	}
	return false
}
