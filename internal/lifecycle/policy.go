package lifecycle

import (
	"errors"
	"fmt"
	"strings"
)

// MetadataPolicy defines how filesystem metadata (modes, owners, xattrs, ACLs)
// is handled during binary transformations.
type MetadataPolicy string

const (
	// PolicyStrict preserves file mode, ownership, and allowed extended attributes,
	// and strictly refuses transformations on inputs with capabilities, integrity
	// claims (IMA/EVM), setuid/setgid bits, or unsupported xattrs.
	PolicyStrict MetadataPolicy = "strict"

	// PolicyStrip strips extended attributes, capabilities, integrity claims,
	// and setuid/setgid bits, producing a clean executable with standard permissions.
	PolicyStrip MetadataPolicy = "strip"

	// DefaultPolicy is the default metadata policy for all transformations.
	DefaultPolicy = PolicyStrict
)

// Common lifecycle transaction errors.
var (
	ErrInvalidMetadataPolicy    = errors.New("invalid metadata policy: must be 'strict' or 'strip'")
	ErrHardLinkDetected         = errors.New("multi-link file detected: in-place replacement severs hard links")
	ErrSourceModified           = errors.New("source file modified or replaced during transformation")
	ErrConcurrentTransformation = errors.New("concurrent transformation in progress on target")
	ErrDestinationExists        = errors.New("destination already exists: publication is create-only")
	ErrUnsafeCapabilities       = errors.New("source has executable capabilities (security.capability)")
	ErrUnsafeIntegrityClaim     = errors.New("source has integrity claims (security.ima/evm)")
	ErrUnsafeSetuidSetgid       = errors.New("source has setuid/setgid bits")
	ErrUnsupportedXattr         = errors.New("source has unsupported extended attributes")
	ErrOwnershipPreservation    = errors.New("cannot preserve file ownership")
	ErrDurabilitySyncFailed     = errors.New("atomic replacement committed, but directory fsync failed")
	ErrReadbackVerification     = errors.New("staged file readback verification failed")
	ErrStagingFailed            = errors.New("failed to stage transformation payload")
)

// Options specifies transaction configuration for a binary transformation.
type Options struct {
	Policy         MetadataPolicy
	BreakHardlinks bool
}

// DefaultOptions returns the safe baseline options for transformations.
func DefaultOptions() Options {
	return Options{
		Policy:         DefaultPolicy,
		BreakHardlinks: false,
	}
}

// ParsePolicy parses and normalizes a metadata policy string.
func ParsePolicy(raw string) (MetadataPolicy, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	switch MetadataPolicy(trimmed) {
	case PolicyStrict, "":
		return PolicyStrict, nil
	case PolicyStrip:
		return PolicyStrip, nil
	default:
		return "", fmt.Errorf("%w: %q (supported: 'strict', 'strip')", ErrInvalidMetadataPolicy, raw)
	}
}
