package memfd

import (
	"errors"
	"os"
)

// ErrUnsupported is returned when memfd operations are attempted on non-Linux platforms
// or unsupported environments.
var ErrUnsupported = errors.New("memfd operations are unsupported on this platform")

// CreateResult captures the metadata and descriptor returned from an executable memfd creation attempt.
type CreateResult struct {
	// FD is the owned file descriptor on success, or -1 on error.
	FD int `json:"fd"`
	// Flags is the final flag combination used for the successful creation.
	Flags int `json:"flags"`
	// ExplicitExecSucceeded is true if the preferred MFD_EXEC attempt succeeded directly.
	ExplicitExecSucceeded bool `json:"explicit_exec_succeeded"`
	// LegacyRetryUsed is true if the initial MFD_EXEC attempt failed with EINVAL and the legacy retry was used.
	LegacyRetryUsed bool `json:"legacy_retry_used"`
	// FirstErr records the original error from the first attempt (e.g. EINVAL) if a retry was performed.
	FirstErr error `json:"-"`
}

// CreationStrategy returns a human-readable description of how the descriptor was created.
func (r CreateResult) CreationStrategy() string {
	if r.ExplicitExecSucceeded {
		return "MFD_EXEC"
	}
	if r.LegacyRetryUsed {
		return "legacy retry (implicit execution)"
	}
	return "standard"
}

// ModeObservation records the observed permission bits of a created descriptor.
type ModeObservation struct {
	Mode         os.FileMode `json:"mode"`
	ModeOctal    string      `json:"mode_octal"`
	IsExecutable bool        `json:"is_executable"`
}

// SealsObservation records the target and observed fcntl seal masks.
type SealsObservation struct {
	TargetMask   int    `json:"target_mask"`
	ObservedMask int    `json:"observed_mask"`
	Matches      bool   `json:"matches"`
	Supported    bool   `json:"supported"`
	Error        string `json:"error,omitempty"`
}
