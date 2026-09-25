//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/lifecycle"
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
	rep.Execution = "unknown (prerequisite check only; prerequisite checks passed, payload execution was not tested)"

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

	obs := lifecycle.CheckMemfdSupport(&adapter)
	rep.Available = obs.Available
	rep.Passed = obs.Passed
	rep.Phase = obs.Phase
	rep.CreationStrategy = obs.CreationStrategy
	rep.Mode = obs.Mode
	rep.Seals = obs.Seals
	rep.Error = obs.Error
	rep.ErrorMessage = obs.ErrorMessage
	rep.ErrnoName = obs.ErrnoName
	rep.ErrnoValue = obs.ErrnoValue
	rep.CandidateExplanations = obs.CandidateExplanations
	rep.Status = obs.Status
	rep.Execution = obs.Execution

	if !obs.Passed {
		rep.Execution = executionUnavailable
		switch {
		case obs.ErrnoName == "EPERM" || obs.ErrnoName == "EACCES":
			rep.Seccomp = "Restricted (EPERM/EACCES)"
		case obs.ErrnoName == "ENOSYS":
			rep.Seccomp = "N/A (ENOSYS)"
		case obs.Phase == "mode":
			rep.Seccomp = "Permitted (creation), execution denied"
		case obs.Phase == "seals":
			rep.Seccomp = "Restricted (seals blocked)"
		default:
			rep.Seccomp = "Unknown"
		}

		switch obs.Phase {
		case "creation":
			rep.Hint = format.DiagnoseError(format.StageMemfdCreate, errors.New(obs.Error))
			if rep.Hint == "" {
				rep.Hint = "memfd_create failed. If disk cache execution is permitted, run with MICROFAT_EXEC_MODE=cache."
			}
		case "mode":
			rep.Hint = "Host system enforces vm.memfd_noexec without execution permissions. Set MICROFAT_EXEC_MODE=cache."
		case "seals":
			rep.Hint = "memfd creation succeeded but descriptor sealing failed. Check seccomp filters or kernel seal support."
		case "fstat":
			rep.Hint = "fstat failed on memfd descriptor. Check kernel status or security policies."
		}
		return rep
	}

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
