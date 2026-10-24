//go:build !linux

package main

import (
	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/lifecycle"
)

func probeMemfd() MemfdReport {
	obs := lifecycle.CheckMemfdSupport(nil)
	return MemfdReport{
		Available: obs.Available,
		Passed:    obs.Passed,
		Status:    obs.Status,
		Seccomp:   "N/A",
		Execution: "not_tested",
	}
}

func readDoctorCgroupLimits() (cgroup.Limits, error) {
	return cgroup.Limits{}, nil
}
