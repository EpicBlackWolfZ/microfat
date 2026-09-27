package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Installation is a read-only observation, not authorization to mutate it.
type Installation struct {
	Owner   Owner
	Running Generation
	Current Generation
}

// ReadInstallation inspects a validated physical CLI path, including both the
// running and active generations. It never creates a lock or repairs links.
func ReadInstallation(physical string) (Installation, error) {
	var result Installation
	if !filepath.IsAbs(physical) || filepath.Clean(physical) != physical ||
		!IsGenerationPath(physical) || filepath.Base(physical) != "microfat" {
		return result, ErrOwnership
	}
	err := inspectDiscoveryGeneration(physical, func(root *os.Root, owner Owner, running Generation) error {
		result.Owner, result.Running = owner, running
		target, err := selectedTarget(root)
		if err != nil || target == "" {
			return errors.Join(ErrChanged, err)
		}
		id := filepath.Base(target)
		if target != filepath.Join(generationDir, id) {
			return ErrOwnership
		}
		result.Current, err = readGenerationForUID(root, id, owner.UID)
		if err != nil {
			return err
		}
		for _, name := range Products() {
			if err := verifyFileForUID(root, filepath.Join(generationDir, id, name), result.Current.Files[name], owner.UID); err != nil {
				return fmt.Errorf("%w: %w", ErrCorrupt, err)
			}
		}
		if err := checkExistingLinks(Paths{Bin: owner.Bin, Store: owner.Store}, owner.UID); err != nil {
			return err
		}
		after, err := selectedTarget(root)
		if err != nil || after != target {
			return errors.Join(ErrChanged, err)
		}
		return nil
	})
	return result, err
}

func checkExistingLinks(paths Paths, uid int) error {
	if err := validateAncestors(paths.Bin, false); err != nil {
		return err
	}
	root, err := os.OpenRoot(paths.Bin)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	if err := validateReadInfo(info, true, uid); err != nil {
		return err
	}
	for _, name := range Products() {
		target, err := root.Readlink(name)
		if err != nil || target != filepath.Join(paths.Store, currentLink, name) {
			return errors.Join(fmt.Errorf("%w: %s", ErrConflict, name), err)
		}
	}
	return nil
}

// InspectUpdate captures an existing, healthy, active installation. A delayed
// process cannot update a generation other than the one from which it started.
func InspectUpdate(physical string) (Snapshot, error) {
	var snapshot Snapshot
	observed, err := ReadInstallation(physical)
	if err != nil {
		return snapshot, err
	}
	if observed.Running.ID != observed.Current.ID {
		return snapshot, fmt.Errorf("%w: restart through the public microfat entrypoint", ErrChanged)
	}
	snapshot, err = Inspect(Paths{Bin: observed.Owner.Bin, Store: observed.Owner.Store})
	if err != nil {
		return snapshot, err
	}
	if snapshot.owner == nil || *snapshot.owner != observed.Owner || snapshot.current == nil ||
		snapshot.current.ID != observed.Current.ID || !sameRelease(*snapshot.current, observed.Current) {
		return snapshot, ErrChanged
	}
	snapshot.updateFiles, err = updateFileIdentities(snapshot)
	if err != nil {
		return snapshot, err
	}
	snapshot.updateOnly = true
	return snapshot, nil
}

func (op *operation) checkUpdateSnapshot(snapshot Snapshot) error {
	if snapshot.owner == nil || snapshot.current == nil {
		return ErrOwnership
	}
	if err := op.checkRoots(); err != nil {
		return err
	}
	if err := op.checkOwner(snapshot.owner); err != nil {
		return err
	}
	current, err := activeGeneration(op.store)
	if err != nil || current.ID != snapshot.current.ID || !sameRelease(current, *snapshot.current) {
		return errors.Join(ErrChanged, err)
	}
	if err := verifyGeneration(op.store, current); err != nil {
		return err
	}
	for name, expected := range snapshot.updateFiles {
		actual, err := op.store.Lstat(name)
		if err != nil || !os.SameFile(expected, actual) {
			return errors.Join(ErrChanged, err)
		}
	}
	return checkExistingLinks(snapshot.paths, snapshot.owner.UID)
}

// Pin inodes as well as contents: replacing a product with identical bytes still
// invalidates the source identity captured by the running updater.
func updateFileIdentities(snapshot Snapshot) (map[string]os.FileInfo, error) {
	root, err := os.OpenRoot(snapshot.paths.Store)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	generation := filepath.Join(generationDir, snapshot.current.ID)
	names := []string{ownerFile, generation, filepath.Join(generation, generationFile)}
	for _, name := range Products() {
		names = append(names, filepath.Join(generation, name))
	}
	files := make(map[string]os.FileInfo, len(names))
	for _, name := range names {
		info, err := root.Lstat(name)
		if err != nil {
			return nil, err
		}
		files[name] = info
	}
	return files, nil
}
