package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrDiscovery prevents callers from silently escaping an invalid managed
// installation by selecting an unrelated companion from PATH.
var ErrDiscovery = errors.New("cannot bind companion discovery to the installed generation")

func validateReadInfo(info os.FileInfo, directory bool, uid int) error {
	if err := validateInfo(info, directory, false); err != nil {
		return err
	}
	if fileUID(info) != uid || info.Mode().Perm()&0o022 != 0 {
		return ErrOwnership
	}
	return nil
}

// IsGenerationPath recognizes the layout even when its metadata or product has
// disappeared. Recognition conveys no authority and never permits a write.
func IsGenerationPath(name string) bool {
	dir := filepath.Dir(name)
	return filepath.Base(filepath.Dir(dir)) == generationDir && idPattern.MatchString(filepath.Base(dir))
}

// ValidateDiscovery validates a physical generation path without consulting the
// moving current pointer. A system installation can be read by an ordinary user,
// but mutations still require the recorded owner. The launcher's original path
// must already identify the physical generation: a payload hash cannot distinguish
// generations that happen to contain identical selected payload bytes.
// Metadata and environment hints establish local consistency, not provenance or
// protection against a hostile process running as the owner (or root).
func ValidateDiscovery(original, physical string) (bool, error) {
	managed := IsGenerationPath(original) || IsGenerationPath(physical)
	if !managed {
		_, err := os.Lstat(filepath.Join(filepath.Dir(physical), generationFile))
		managed = err == nil || !errors.Is(err, os.ErrNotExist)
	}
	if !managed {
		return false, nil
	}
	if original != physical || !filepath.IsAbs(physical) || filepath.Clean(physical) != physical ||
		!IsGenerationPath(physical) || filepath.Base(physical) != "microfat" {
		return true, fmt.Errorf("%w: launch must identify a physical generation, not a moving entrypoint", ErrDiscovery)
	}
	if err := validateDiscoveryGeneration(physical); err != nil {
		return true, fmt.Errorf("%w: %w", ErrDiscovery, err)
	}
	return true, nil
}

func validateDiscoveryGeneration(physical string) error {
	return inspectDiscoveryGeneration(physical, nil)
}

func inspectDiscoveryGeneration(physical string, inspect func(*os.Root, Owner, Generation) error) error {
	dir := filepath.Dir(physical)
	store := filepath.Dir(filepath.Dir(dir))
	if err := validateAncestors(dir, false); err != nil {
		return err
	}
	root, err := os.OpenRoot(store)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	uid := fileUID(info)
	for _, name := range []string{".", generationDir} {
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if err := validateReadInfo(info, true, uid); err != nil {
			return err
		}
	}
	var owner Owner
	if err := readJSONForUID(root, ownerFile, &owner, uid); err != nil {
		return err
	}
	if owner.Schema != SchemaVersion || owner.Kind != OwnerKind || !idPattern.MatchString(owner.ID) ||
		owner.UID != uid || owner.Store != store {
		return ErrOwnership
	}
	if err := (Paths{Bin: owner.Bin, Store: store}).Validate(); err != nil {
		return err
	}
	generation, err := readGenerationForUID(root, filepath.Base(dir), uid)
	if err != nil {
		return err
	}
	for _, name := range Products() {
		if err := verifyFileForUID(root, filepath.Join(generationDir, generation.ID, name), generation.Files[name], uid); err != nil {
			return err
		}
	}
	if inspect != nil {
		return inspect(root, owner, generation)
	}
	return nil
}
