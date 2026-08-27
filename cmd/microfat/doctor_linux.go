//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"golang.org/x/sys/unix"
)

var (
	memfdProbeSyscall    = unix.MemfdCreate
	unameSyscall         = unix.Uname
	readCgroupLimitsFunc = cgroup.ReadLimits
)

func probeMemfd() MemfdReport {
	var rep MemfdReport
	var uts unix.Utsname
	if err := unameSyscall(&uts); err == nil {
		rep.Kernel = fmt.Sprintf("Linux %s", unix.ByteSliceToString(uts.Release[:]))
	} else {
		rep.Kernel = "Linux (unknown release)"
	}

	fd, err := memfdProbeSyscall("microfat_doctor_probe", unix.MFD_CLOEXEC)
	if err != nil {
		rep.Available = false
		rep.Error = err.Error()
		rep.Hint = format.DiagnoseError(format.StageMemfdCreate, err)
		if rep.Hint == "" {
			rep.Hint = "memfd_create failed. Fallback to cache will be used. Set MICROFAT_EXEC_MODE=cache to skip memfd probe."
		}
		if errno, ok := err.(syscall.Errno); ok {
			switch errno {
			case unix.EPERM, unix.EACCES:
				rep.Status = "Blocked by host seccomp/security profile"
				rep.Seccomp = "Restricted (EPERM/EACCES)"
			case unix.ENOSYS:
				rep.Status = "Unsupported by Linux kernel (< 3.17)"
				rep.Seccomp = "N/A (ENOSYS)"
			default:
				rep.Status = fmt.Sprintf("Failed (%v)", err)
				rep.Seccomp = "Unknown"
			}
		} else {
			rep.Status = fmt.Sprintf("Failed (%v)", err)
			rep.Seccomp = "Unknown"
		}
		return rep
	}

	_ = unix.Close(fd)
	rep.Available = true
	rep.Status = "Available"
	rep.Seccomp = "Permitted"
	return rep
}

func probeCgroup() *CgroupReport {
	limits, err := readCgroupLimitsFunc()
	if err != nil || limits.CgroupVersion == cgroup.VersionUnknown {
		return &CgroupReport{
			Detected: false,
			Version:  cgroup.VersionUnknown,
		}
	}

	plan := cgroup.ResolveTuningPlan(limits, os.Getenv(format.EnvMemRatio), cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes)

	return &CgroupReport{
		Detected:         true,
		Version:          limits.CgroupVersion,
		MemoryLimitBytes: limits.MemoryLimitBytes,
		CPUQuota:         limits.CPUQuota,
		GOMEMLIMITBytes:  plan.GOMEMLIMITBytes,
		GOMEMLIMITStr:    plan.GOMEMLIMITStr,
		GOMAXPROCS:       plan.GOMAXPROCS,
	}
}
