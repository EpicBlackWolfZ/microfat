//go:build !linux

package main

import (
	"fmt"
	"runtime"
)

func probeMemfd() MemfdReport {
	return MemfdReport{
		Available: false,
		Status:    fmt.Sprintf("N/A (%s host; microfat fat execution targets Linux ELF)", runtime.GOOS),
		Seccomp:   "N/A",
	}
}

func probeCgroup() *CgroupReport {
	return &CgroupReport{
		Detected: false,
		Version:  0,
	}
}
