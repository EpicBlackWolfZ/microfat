//go:build !linux

package lifecycle

import (
	"fmt"
	"runtime"

	"github.com/EpicBlackWolfZ/microfat/internal/memfd"
)

// MemfdSupportObservation records the outcome of host memfd prerequisite checks.
type MemfdSupportObservation struct {
	Available             bool
	Passed                bool
	Phase                 string
	Status                string
	CreationStrategy      string
	Mode                  *memfd.ModeObservation
	Seals                 *memfd.SealsObservation
	Execution             string
	Error                 string
	ErrorMessage          string
	ErrnoName             string
	ErrnoValue            int
	CandidateExplanations []string
	Hint                  string
}

// CheckMemfdSupport reports unsupported on non-Linux platforms.
func CheckMemfdSupport(adapter *memfd.SyscallAdapter) MemfdSupportObservation {
	return MemfdSupportObservation{
		Available: false,
		Passed:    false,
		Status:    fmt.Sprintf("N/A (%s host; microfat fat execution targets Linux ELF)", runtime.GOOS),
		Execution: "unavailable",
	}
}
