//go:build linux

package main

import (
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
	rep.Operation = obs.Operation
	rep.CreationStrategy = obs.CreationStrategy
	rep.Mode = obs.Mode
	rep.Seals = obs.Seals
	rep.Error = obs.Error
	rep.ErrorMessage = obs.ErrorMessage
	rep.ErrnoName = obs.ErrnoName
	rep.ErrnoValue = obs.ErrnoValue
	rep.CandidateExplanations = obs.CandidateExplanations
	rep.Status = obs.Status
	rep.Execution = "not_tested"
	rep.Hint = obs.Hint
	rep.Cause = obs.Cause
	rep.Seccomp = "Unknown (not independently tested)"

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
