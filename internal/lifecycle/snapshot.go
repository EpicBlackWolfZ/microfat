package lifecycle

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	attrSecurityCapability = "security.capability"
	attrSecurityIMA        = "security.ima"
	attrSecurityEVM        = "security.evm"
	attrSecuritySELinux    = "security.selinux"
	attrUserPrefix         = "user."
)

// SourceSnapshot records physical filesystem metadata and extended attributes
// of the source executable before beginning a transformation.
type SourceSnapshot struct {
	Path    string
	Dev     uint64
	Ino     uint64
	Mode    os.FileMode
	UID     int
	GID     int
	Nlink   uint64
	Size    int64
	ModTime time.Time
	Xattrs  map[string][]byte
}

// TakeSourceSnapshot captures identity, mode, ownership, and extended attributes
// from an open file descriptor or file path.
func TakeSourceSnapshot(f *os.File, path string) (*SourceSnapshot, error) {
	var fi os.FileInfo
	var err error
	if f != nil {
		fi, err = f.Stat()
	} else {
		fi, err = os.Stat(path)
	}
	if err != nil {
		return nil, fmt.Errorf("stating source executable %s: %w", path, err)
	}

	dev, ino, nlink, uid, gid, ok := fileStatMetadataFunc(fi)
	if !ok {
		// Fallback for non-UNIX platforms where raw stat is unavailable
		dev, ino, nlink = 0, 0, 1
		uid, gid = os.Geteuid(), os.Getegid()
	}

	xattrs, err := readXattrsFunc(path)
	if err != nil {
		return nil, fmt.Errorf("reading extended attributes from %s: %w", path, err)
	}

	return &SourceSnapshot{
		Path:    path,
		Dev:     dev,
		Ino:     ino,
		Mode:    fi.Mode(),
		UID:     uid,
		GID:     gid,
		Nlink:   nlink,
		Size:    fi.Size(),
		ModTime: fi.ModTime(),
		Xattrs:  xattrs,
	}, nil
}

// ValidateSnapshot verifies that the source file metadata can be safely preserved
// under the chosen metadata policy.
func ValidateSnapshot(snap *SourceSnapshot, policy MetadataPolicy) error {
	if snap == nil {
		return nil
	}

	if policy != PolicyStrict {
		// Strip mode strips unsafe attributes and elevated bits; everything is acceptable.
		return nil
	}

	// 1. Refuse setuid / setgid bits in strict mode
	if snap.Mode&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return fmt.Errorf("%w: file %s has elevated bits %v; use --metadata-policy=strip to strip",
			ErrUnsafeSetuidSetgid, snap.Path, snap.Mode)
	}

	// 2. Inspect extended attributes
	for attr := range snap.Xattrs {
		switch {
		case attr == attrSecurityCapability:
			return fmt.Errorf("%w: file %s carries file capabilities; use --metadata-policy=strip to discard",
				ErrUnsafeCapabilities, snap.Path)
		case attr == attrSecurityIMA || attr == attrSecurityEVM:
			return fmt.Errorf("%w: file %s carries integrity claim %q; use --metadata-policy=strip to discard",
				ErrUnsafeIntegrityClaim, snap.Path, attr)
		case attr == attrSecuritySELinux || strings.HasPrefix(attr, attrUserPrefix):
			// Supported and safe to preserve
			continue
		default:
			return fmt.Errorf("%w: file %s carries unapproved attribute %q; use --metadata-policy=strip to discard",
				ErrUnsupportedXattr, snap.Path, attr)
		}
	}

	return nil
}
