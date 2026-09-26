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
	Operation             string
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
	Cause                 error `json:"-"`
}

// CheckMemfdSupport checks kernel memfd_create, fstat mode, and mandatory sealing prerequisites.
// In all paths, the test descriptor is closed cleanly without leaking or double-closing.
// If creation, fstat, mode inspection, or sealing fails, Available and Passed are false,
// and the observation retains the failure phase, operation, error message, and syscall.Errno.
func CheckMemfdSupport(adapter *memfd.SyscallAdapter) MemfdSupportObservation {
	var obs MemfdSupportObservation
	obs.Execution = ExecutionNotTested

	res, err := memfd.CreateExecutable("microfat_doctor_probe", adapter)
	if err != nil {
		obs.Available = false
		obs.Passed = false
		obs.Phase = "creation"
		obs.Operation = "create"
		obs.Execution = ExecutionNotTested
		obs.Cause = err
		obs.Error = err.Error()
		obs.ErrorMessage = err.Error()
		obs.ErrnoName, obs.ErrnoValue, obs.CandidateExplanations = ResolveCandidateExplanations(err)
		switch {
		case obs.ErrnoName == "EPERM" || obs.ErrnoName == "EACCES":
			obs.Status = "Creation denied (permission denied by kernel or security policy)"
			obs.Hint = "memfd creation denied. If disk cache execution is permitted, run with MICROFAT_EXEC_MODE=cache."
		case obs.ErrnoName == "ENOSYS":
			obs.Status = "Creation unsupported (memfd_create system call not implemented by this kernel)"
			obs.Hint = "memfd_create not implemented. If disk cache execution is permitted, run with MICROFAT_EXEC_MODE=cache."
		default:
			obs.Status = fmt.Sprintf("Failed during %s (%v)", obs.Phase, err)
			obs.Hint = "memfd_create failed. If disk cache execution is permitted, run with MICROFAT_EXEC_MODE=cache."
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
		obs.Operation = "fstat"
		obs.Cause = modeErr
		obs.Error = modeErr.Error()
		obs.ErrorMessage = modeErr.Error()
		obs.ErrnoName, obs.ErrnoValue, obs.CandidateExplanations = ResolveCandidateExplanations(modeErr)
		obs.Status = fmt.Sprintf("Descriptor inspection failed during %s: %v", obs.Phase, modeErr)
		obs.Hint = "fstat failed on memfd descriptor. Check kernel status or security policies."
		obs.Execution = ExecutionNotTested
		return obs
	}

	if !modeObs.IsExecutable {
		obs.Available = false
		obs.Passed = false
		obs.Phase = "mode"
		obs.Operation = "mode"
		obs.Status = "Descriptor non-executable (descriptor lacks executable permission bits)"
		obs.Error = "memfd descriptor lacks executable permission bits"
		obs.ErrorMessage = obs.Error
		obs.CandidateExplanations = []string{
			"Descriptor created without executable permission bits",
		}
		obs.Hint = "Descriptor lacks execution bits. If disk cache execution is permitted, run with MICROFAT_EXEC_MODE=cache."
		obs.Execution = ExecutionNotTested
		return obs
	}

	if sealsErr != nil || !sealsObs.Matches {
		obs.Available = false
		obs.Passed = false
		obs.Phase = "seals"
		obs.Execution = ExecutionNotTested
		if sealsErr != nil {
			obs.Cause = sealsErr
			obs.Error = sealsErr.Error()
			obs.ErrorMessage = sealsErr.Error()
			obs.ErrnoName, obs.ErrnoValue, obs.CandidateExplanations = ResolveCandidateExplanations(sealsErr)
			if strings.Contains(sealsErr.Error(), "applying target seals") {
				obs.Operation = "add_seals"
				obs.Status = "Seal application denied (F_ADD_SEALS failed)"
				obs.Hint = "memfd creation succeeded but seal application (F_ADD_SEALS) was denied. Check security policies or kernel seal support."
			} else {
				obs.Operation = "get_seals"
				obs.Status = "Seal verification failed (F_GET_SEALS failed)"
				obs.Hint = "memfd creation and sealing succeeded, but seal verification (F_GET_SEALS) failed. Check kernel status or security policies."
			}
		} else {
			obs.Operation = "get_seals"
			obs.Status = "Seal verification failed (mandatory seals missing in readback)"
			obs.Error = "mandatory seals missing in readback"
			obs.ErrorMessage = obs.Error
			obs.CandidateExplanations = []string{
				"Mandatory seals (F_SEAL_WRITE|F_SEAL_SHRINK|F_SEAL_GROW|F_SEAL_SEAL) could not be verified in seal mask",
			}
			obs.Hint = "Mandatory seals missing in readback. Check kernel seal support."
		}
		return obs
	}

	obs.Available = true
	obs.Passed = true
	obs.Phase = ""
	obs.Operation = ""
	obs.Status = "Available"
	obs.Execution = ExecutionNotTested
	return obs
}
