//go:build linux

package lifecycle

import (
	"fmt"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/memfd"
	"golang.org/x/sys/unix"
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

// CheckMemfdSupport checks kernel memfd_create, fstat mode, and mandatory sealing prerequisites.
// In all paths, the test descriptor is closed cleanly without leaking or double-closing.
// If creation, fstat, mode inspection, or sealing fails, Available and Passed are false,
// and the observation retains the failure phase, error message, and syscall.Errno.
func CheckMemfdSupport(adapter *memfd.SyscallAdapter) MemfdSupportObservation {
	var obs MemfdSupportObservation
	obs.Execution = "unknown (prerequisite check only; prerequisite checks passed, payload execution was not tested)"

	res, err := memfd.CreateExecutable("microfat_doctor_probe", adapter)
	if err != nil {
		obs.Available = false
		obs.Passed = false
		obs.Phase = "creation"
		obs.Execution = "unavailable"
		obs.Error = err.Error()
		obs.ErrorMessage = err.Error()
		obs.ErrnoName, obs.ErrnoValue, obs.CandidateExplanations = ResolveCandidateExplanations(err)
		switch {
		case obs.ErrnoName == "EPERM" || obs.ErrnoName == "EACCES":
			obs.Status = "Blocked (EPERM/EACCES; possible seccomp or security policy restriction)"
		case obs.ErrnoName == "ENOSYS":
			obs.Status = "Unsupported by Linux kernel (ENOSYS)"
		default:
			obs.Status = fmt.Sprintf("Failed during %s (%v)", obs.Phase, err)
		}
		return obs
	}

	defer func() {
		if adapter != nil {
			if adapter.Close != nil {
				_ = adapter.Close(res.FD)
			}
			return
		}
		_ = unix.Close(res.FD)
	}()

	obs.CreationStrategy = res.CreationStrategy()

	modeObs, modeErr := memfd.CheckExecutableMode(res.FD, adapter)
	if modeErr == nil {
		obs.Mode = &modeObs
	}

	sealsObs, sealsErr := memfd.VerifySeals(res.FD, adapter)
	obs.Seals = &sealsObs

	if modeErr != nil {
		obs.Available = false
		obs.Passed = false
		obs.Phase = "fstat"
		obs.Error = modeErr.Error()
		obs.ErrorMessage = modeErr.Error()
		obs.ErrnoName, obs.ErrnoValue, obs.CandidateExplanations = ResolveCandidateExplanations(modeErr)
		obs.Status = fmt.Sprintf("Descriptor inspection failed during %s: %v", obs.Phase, modeErr)
		obs.Execution = "unavailable (mode inspection failed)"
		return obs
	}

	if !modeObs.IsExecutable {
		obs.Available = false
		obs.Passed = false
		obs.Phase = "mode"
		obs.Status = "Descriptor non-executable (possible cause: vm.memfd_noexec or MFD_NOEXEC)"
		obs.Error = "memfd descriptor lacks executable permission bits"
		obs.ErrorMessage = obs.Error
		obs.CandidateExplanations = []string{
			"Descriptor created without executable permission bits (vm.memfd_noexec or MFD_NOEXEC_SEAL in effect)",
		}
		obs.Execution = "unavailable (non-executable descriptor)"
		return obs
	}

	if sealsErr != nil || !sealsObs.Matches {
		obs.Available = false
		obs.Passed = false
		obs.Phase = "seals"
		if sealsErr != nil {
			obs.Error = sealsErr.Error()
			obs.ErrorMessage = sealsErr.Error()
			obs.ErrnoName, obs.ErrnoValue, obs.CandidateExplanations = ResolveCandidateExplanations(sealsErr)
			if strings.Contains(sealsErr.Error(), "applying target seals") {
				obs.Status = "Sealing failed (F_ADD_SEALS blocked or unsupported)"
			} else {
				obs.Status = "Sealing verification failed (F_GET_SEALS failed)"
			}
		} else {
			obs.Status = "Sealing verification failed (mandatory seals missing in readback)"
			obs.Error = "mandatory seals missing in readback"
			obs.ErrorMessage = obs.Error
			obs.CandidateExplanations = []string{
				"Mandatory seals (F_SEAL_WRITE|F_SEAL_SHRINK|F_SEAL_GROW|F_SEAL_SEAL) could not be verified in seal mask",
			}
		}
		obs.Execution = "unavailable (sealing failed)"
		return obs
	}

	obs.Available = true
	obs.Passed = true
	obs.Phase = ""
	obs.Status = "Available"
	return obs
}
