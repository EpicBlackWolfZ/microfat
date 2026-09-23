package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	stagingFilePattern = ".microfat-tx-*.tmp"
	defaultStagingMode = 0o600
	defaultDirMode     = 0o755
	standardExecMask   = 0o755
	ownerExecuteMask   = 0o100
)

var (
	renameFunc  = os.Rename
	geteuidFunc = os.Geteuid
)

// TransformFunc defines the transformation callback that writes the payload
// into the temporary staging file.
type TransformFunc func(staged *os.File) error

// Transaction specifies parameters for a safe filesystem transformation.
type Transaction struct {
	SrcPath   string
	SrcFile   *os.File
	DestPath  string
	Opts      Options
	Transform TransformFunc
}

// Execute performs an atomic, metadata-preserving binary transformation.
func Execute(tx Transaction) error {
	if tx.Transform == nil {
		return errors.New("transformation callback must not be nil")
	}
	if tx.SrcPath == "" {
		return errors.New("source path must not be empty")
	}

	policy, err := ParsePolicy(string(tx.Opts.Policy))
	if err != nil {
		return err
	}
	tx.Opts.Policy = policy

	snap, destPath, inPlace, err := resolveTargetAndSnapshot(&tx)
	if err != nil {
		return err
	}

	if inPlace {
		unlock, lockErr := acquireLockFunc(destPath)
		if lockErr != nil {
			return lockErr
		}
		defer unlock()
	}

	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, defaultDirMode); err != nil {
		return fmt.Errorf("creating directory %s: %w", destDir, err)
	}

	staged, err := os.CreateTemp(destDir, stagingFilePattern)
	if err != nil {
		return fmt.Errorf("%w: creating temporary file in %s: %w", ErrStagingFailed, destDir, err)
	}
	stagedPath := staged.Name()

	committed := false
	defer func() {
		if !committed {
			_ = staged.Close()
			_ = os.Remove(stagedPath)
		}
	}()

	if err := chmodFunc(staged, defaultStagingMode); err != nil {
		return fmt.Errorf("%w: setting staging permissions: %w", ErrStagingFailed, err)
	}

	if err := tx.Transform(staged); err != nil {
		return fmt.Errorf("transformation callback failed: %w", err)
	}

	targetMode, err := applyMetadata(staged, snap, tx.Opts.Policy)
	if err != nil {
		return err
	}

	if err := verifyReadback(staged, targetMode, snap, tx.Opts.Policy); err != nil {
		return err
	}

	if err := staged.Sync(); err != nil {
		return fmt.Errorf("syncing staged file: %w", err)
	}
	if err := staged.Close(); err != nil {
		return fmt.Errorf("closing staged file: %w", err)
	}

	if err := publishTransaction(inPlace, stagedPath, destPath, snap); err != nil {
		return err
	}
	committed = true

	if err := syncDirFunc(destDir); err != nil {
		return fmt.Errorf("%w: %w", ErrDurabilitySyncFailed, err)
	}
	return nil
}

func resolveTargetAndSnapshot(tx *Transaction) (*SourceSnapshot, string, bool, error) {
	cleanSrc := filepath.Clean(tx.SrcPath)
	snap, err := TakeSourceSnapshot(tx.SrcFile, cleanSrc)
	if err != nil {
		return nil, "", false, err
	}
	if err := ValidateSnapshot(snap, tx.Opts.Policy); err != nil {
		return nil, "", false, err
	}

	inPlace := false
	destPath := filepath.Clean(tx.DestPath)
	if tx.DestPath == "" || destPath == cleanSrc {
		inPlace = true
		destPath = cleanSrc
	} else if destFi, err := os.Stat(destPath); err == nil {
		dev, ino, _, _, _, ok := fileStatMetadataFunc(destFi)
		if ok && dev == snap.Dev && ino == snap.Ino {
			inPlace = true
		}
	}

	if inPlace {
		if realSrc, err := filepath.EvalSymlinks(cleanSrc); err == nil && realSrc != cleanSrc {
			destPath = realSrc
			if realFi, err := os.Stat(realSrc); err == nil {
				dev, ino, _, _, _, ok := fileStatMetadataFunc(realFi)
				if ok && (dev != snap.Dev || ino != snap.Ino) {
					snap, err = TakeSourceSnapshot(nil, realSrc)
					if err != nil {
						return nil, "", false, err
					}
					if err := ValidateSnapshot(snap, tx.Opts.Policy); err != nil {
						return nil, "", false, err
					}
				}
			}
		}
		if snap.Nlink > 1 && !tx.Opts.BreakHardlinks {
			return nil, "", false, fmt.Errorf("%w: %s has %d links; pass --break-hardlinks to break link",
				ErrHardLinkDetected, cleanSrc, snap.Nlink)
		}
	}

	return snap, destPath, inPlace, nil
}

func applyMetadata(staged *os.File, snap *SourceSnapshot, policy MetadataPolicy) (os.FileMode, error) {
	if policy == PolicyStrict {
		if err := chownFunc(staged, snap.UID, snap.GID); err != nil {
			if geteuidFunc() != snap.UID || os.Getegid() != snap.GID {
				return 0, fmt.Errorf("%w: cannot preserve uid=%d gid=%d (running as uid=%d): use --metadata-policy=strip: %w",
					ErrOwnershipPreservation, snap.UID, snap.GID, geteuidFunc(), err)
			}
		}
		for attr, val := range snap.Xattrs {
			if err := setFdXattrFunc(int(staged.Fd()), attr, val); err != nil {
				return 0, fmt.Errorf("setting extended attribute %q: %w", attr, err)
			}
		}
	}

	targetMode := snap.Mode.Perm()
	if policy == PolicyStrip {
		targetMode &= standardExecMask
		if targetMode&ownerExecuteMask == 0 {
			targetMode |= standardExecMask
		}
	}

	if err := chmodFunc(staged, targetMode); err != nil {
		return 0, fmt.Errorf("chmodding staged file to %v: %w", targetMode, err)
	}
	return targetMode, nil
}

func verifyReadback(staged *os.File, targetMode os.FileMode, snap *SourceSnapshot, policy MetadataPolicy) error {
	stagedFi, err := staged.Stat()
	if err != nil {
		return fmt.Errorf("%w: stating staged file: %w", ErrReadbackVerification, err)
	}
	if stagedFi.Size() == 0 {
		return fmt.Errorf("%w: staged file is empty", ErrReadbackVerification)
	}
	if stagedFi.Mode().Perm() != targetMode {
		return fmt.Errorf("%w: mode %v does not match target %v",
			ErrReadbackVerification, stagedFi.Mode().Perm(), targetMode)
	}
	if policy == PolicyStrict && geteuidFunc() == 0 {
		_, _, _, uid, gid, ok := fileStatMetadataFunc(stagedFi)
		if ok && (uid != snap.UID || gid != snap.GID) {
			return fmt.Errorf("%w: ownership %d:%d does not match %d:%d",
				ErrReadbackVerification, uid, gid, snap.UID, snap.GID)
		}
	}
	return nil
}

func publishTransaction(inPlace bool, stagedPath, destPath string, snap *SourceSnapshot) error {
	if inPlace {
		currentFi, err := os.Stat(destPath)
		if err != nil {
			return fmt.Errorf("%w: verifying target before replacement: %w", ErrSourceModified, err)
		}
		dev, ino, _, _, _, ok := fileStatMetadataFunc(currentFi)
		if !ok || dev != snap.Dev || ino != snap.Ino || currentFi.Size() != snap.Size || !currentFi.ModTime().Equal(snap.ModTime) {
			return fmt.Errorf("%w: %s was modified on disk during transformation", ErrSourceModified, destPath)
		}
		if err := renameFunc(stagedPath, destPath); err != nil {
			return fmt.Errorf("replacing %s: %w", destPath, err)
		}
		return nil
	}
	return publishCreateOnlyFunc(stagedPath, destPath)
}
