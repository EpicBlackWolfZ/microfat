package lifecycle

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// ExecutionNotTested indicates that doctor evaluated prerequisites only and did not execute a runtime process.
const ExecutionNotTested = "not_tested"

// PrerequisiteVerdict evaluates environment prerequisite readiness across CPU, memfd, and cache.
type PrerequisiteVerdict struct {
	Ready    bool
	Summary  string
	Warnings []string
	Errors   []string
}

// EvaluatePrerequisites computes the overall prerequisite verdict using a typed truth table:
// - Normal mode: supported CPU architecture AND at least one execution path (memfd OR cache).
// - Strict mode: supported CPU architecture AND memfd available AND cache available.
// A failure in an observed check sets that check's Passed to false and propagates to the summary.
func EvaluatePrerequisites(cpuLevel string, memfdPassed bool, cachePassed bool, strict bool, baseWarnings []string) PrerequisiteVerdict {
	cpuOK := cpuLevel != ""
	var ready bool
	var errs []string
	warnings := make([]string, len(baseWarnings))
	copy(warnings, baseWarnings)

	if !cpuOK {
		errs = append(errs, "Host CPU microarchitecture level is unsupported or could not be detected")
	}

	if strict {
		ready = cpuOK && memfdPassed && cachePassed
		if !memfdPassed {
			errs = append(errs, "Strict mode requirement failed: in-memory memfd_create prerequisites failed")
		}
		if !cachePassed {
			errs = append(errs, "Strict mode requirement failed: disk cache prerequisites failed")
		}
	} else {
		ready = cpuOK && (memfdPassed || cachePassed)
		if cpuOK {
			switch {
			case !memfdPassed && !cachePassed:
				errs = append(errs, "No execution paths available: in-memory memfd_create and disk cache prerequisites both failed")
			case !memfdPassed:
				warnings = append(warnings, "In-memory memfd_create prerequisites failed: fallback to disk cache will be required")
			case !cachePassed:
				warnings = append(warnings, "Disk cache prerequisites failed: fallback will fail if memfd is restricted at runtime")
			}
		}
	}

	var summary string
	if ready {
		if len(warnings) > 0 {
			summary = "Environment prerequisite checks passed with warnings, payload execution was not tested."
		} else {
			summary = "Environment prerequisite checks passed, payload execution was not tested."
		}
	} else {
		summary = "Environment is NOT ready for Microfat execution. Please resolve the errors above."
	}

	return PrerequisiteVerdict{
		Ready:    ready,
		Summary:  summary,
		Warnings: warnings,
		Errors:   errs,
	}
}

// ResolveCandidateExplanations inspects an error and extracts the errno name, value,
// and conditionally formatted candidate explanations according to DOC-2.
func ResolveCandidateExplanations(err error) (string, int, []string) {
	if err == nil {
		return "", 0, nil
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		errnoValue := int(errno)
		errnoName := unix.ErrnoName(errno)
		if errnoName == "" {
			errnoName = fmt.Sprintf("ERRNO_%d", errnoValue)
		}
		switch errno {
		case unix.EPERM:
			return errnoName, errnoValue, []string{
				"Permission denied (possible cause: restricted security policy, container seccomp filter, or LSM confinement)",
			}
		case unix.ENOSYS:
			return errnoName, errnoValue, []string{
				"Function not implemented (memfd_create system call not supported by this kernel)",
			}
		case unix.EINVAL:
			return errnoName, errnoValue, []string{
				"Invalid argument (possible cause: unsupported flags for this kernel)",
			}
		default:
			return errnoName, errnoValue, []string{
				fmt.Sprintf("%s (%d): %s", errnoName, errnoValue, err.Error()),
			}
		}
	}
	return "", 0, []string{err.Error()}
}
