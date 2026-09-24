//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/memfd"
	"golang.org/x/sys/unix"
)

var (
	memfdProbeSyscall    = unix.MemfdCreate
	memfdProbeFstat      = unix.Fstat
	memfdProbeFcntl      = unix.FcntlInt
	memfdProbeClose      = unix.Close
	unameSyscall         = unix.Uname
	readCgroupLimitsFunc = cgroup.ReadLimits
)

func probeMemfd() MemfdReport {
	var rep MemfdReport
	rep.Execution = "unknown (prerequisite check only; process execution not tested)"

	var uts unix.Utsname
	if err := unameSyscall(&uts); err == nil {
		rep.Kernel = fmt.Sprintf("Linux %s", unix.ByteSliceToString(uts.Release[:]))
	} else {
		rep.Kernel = "Linux (unknown release)"
	}

	adapter := memfd.SyscallAdapter{
		MemfdCreate: memfdProbeSyscall,
		Fstat:       memfdProbeFstat,
		FcntlInt:    memfdProbeFcntl,
		Close:       memfdProbeClose,
	}

	res, err := memfd.CreateExecutable("microfat_doctor_probe", &adapter)
	if err != nil {
		rep.Available = false
		rep.Execution = executionUnavailable
		rep.Error = err.Error()
		rep.Hint = format.DiagnoseError(format.StageMemfdCreate, err)
		if rep.Hint == "" {
			rep.Hint = "memfd_create failed. If disk cache execution is permitted, run with MICROFAT_EXEC_MODE=cache."
		}
		if errno, ok := err.(syscall.Errno); ok {
			switch errno {
			case unix.EPERM, unix.EACCES:
				rep.Status = "Blocked by host seccomp/security profile"
				rep.Seccomp = "Restricted (EPERM/EACCES)"
			case unix.ENOSYS:
				rep.Status = "Unsupported by Linux kernel (ENOSYS)"
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

	defer func() {
		if memfdProbeClose != nil {
			_ = memfdProbeClose(res.FD)
		} else {
			_ = unix.Close(res.FD)
		}
	}()

	rep.CreationStrategy = res.CreationStrategy()

	modeObs, modeErr := memfd.CheckExecutableMode(res.FD, &adapter)
	if modeErr == nil {
		rep.Mode = &modeObs
	}

	sealsObs, sealsErr := memfd.VerifySeals(res.FD, &adapter)
	rep.Seals = &sealsObs

	if modeErr == nil && !modeObs.IsExecutable {
		rep.Available = false
		rep.Status = "Descriptor non-executable (MFD_NOEXEC enforced)"
		rep.Seccomp = "Permitted (creation), execution denied"
		rep.Error = "memfd descriptor lacks executable permission bits"
		rep.Hint = "Host system enforces vm.memfd_noexec without execution permissions. Set MICROFAT_EXEC_MODE=cache."
		rep.Execution = "unavailable (non-executable descriptor)"
		return rep
	}

	if sealsErr != nil || !sealsObs.Matches {
		rep.Available = false
		rep.Status = "Sealing failed (F_ADD_SEALS blocked or unsupported)"
		rep.Seccomp = "Restricted (seals blocked)"
		if sealsErr != nil {
			rep.Error = sealsErr.Error()
		} else {
			rep.Error = sealsObs.Error
		}
		rep.Hint = "memfd creation succeeded but descriptor sealing failed. Check seccomp filters or kernel seal support."
		rep.Execution = "unavailable (sealing failed)"
		return rep
	}

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
		Detected:                  true,
		Version:                   limits.CgroupVersion,
		MemoryLimitBytes:          limits.MemoryLimitBytes,
		MemoryHighBytes:           limits.MemoryHighBytes,
		EffectiveMemoryLimitBytes: limits.EffectiveMemoryLimitBytes,
		ConstrainingLimit:         plan.ConstrainingLimit,
		CPUQuota:                  limits.CPUQuota,
		GOMEMLIMITBytes:           plan.GOMEMLIMITBytes,
		GOMEMLIMITStr:             plan.GOMEMLIMITStr,
		GOMAXPROCS:                plan.GOMAXPROCS,
	}
}
