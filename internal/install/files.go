package install

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func NewID() string {
	const bytes = 16
	var id [bytes]byte
	_, _ = rand.Read(id[:]) // crypto/rand.Read always fills the buffer or terminates the process.
	return hex.EncodeToString(id[:])
}

func validateAncestors(name string) error {
	for {
		info, err := os.Lstat(name)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			if err := validateInfo(info, true, false); err != nil {
				return fmt.Errorf("unsafe directory %q: %w", name, err)
			}
		}
		parent := filepath.Dir(name)
		if parent == name {
			return nil
		}
		name = parent
	}
}

// ValidateStagingParent applies the same ancestor ownership policy to executable
// download staging. A private child alone is insufficient if another UID can
// rename it from a writable non-sticky parent before verifier/helper execution.
func ValidateStagingParent(name string) error {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return errors.New("staging parent must be a clean absolute directory")
	}
	return validateAncestors(name)
}

// ValidateVerifierExecutable prevents a different UID from replacing a pinned
// executable between its digest check and execution. It does not authenticate bytes.
func ValidateVerifierExecutable(name string) error {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return errors.New("verifier must have a clean absolute executable path")
	}
	if err := validateAncestors(filepath.Dir(name)); err != nil {
		return err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if err := validateInfo(info, false, false); err != nil {
		return err
	}
	if info.Mode().Perm()&0o111 == 0 {
		return errors.New("pinned verifier is not executable")
	}
	return nil
}

func rootInfo(name string) (os.FileInfo, error) {
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := validateInfo(info, true, true); err != nil {
		return nil, fmt.Errorf("unsafe installation root %q: %w", name, err)
	}
	return info, nil
}

func readJSON(root *os.Root, name string, dest any) error {
	return readJSONForUID(root, name, dest, os.Geteuid())
}

func readJSONForUID(root *os.Root, name string, dest any, uid int) error {
	file, err := root.OpenFile(name, readFlags(), 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := validateReadInfo(info, false, uid); err != nil {
		return err
	}
	if info.Size() > metadataLimit {
		return errors.New("installation metadata exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, metadataLimit+1))
	if err != nil {
		return err
	}
	if len(data) > metadataLimit {
		return errors.New("installation metadata exceeds size limit")
	}
	return json.Unmarshal(data, dest, json.RejectUnknownMembers(true))
}

func writeJSON(root *os.Root, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := root.OpenFile(name, writeFlags(), publicMetadataMode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Chmod(publicMetadataMode), file.Sync(), file.Close())
}

func publishableDirectory(root *os.Root, name string) error {
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(file.Chmod(directoryMode), file.Sync(), file.Close())
}

func syncDir(root *os.Root, name string) error {
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func readOwner(root *os.Root, paths Paths) (*Owner, error) {
	var owner Owner
	if err := readJSON(root, ownerFile, &owner); err != nil {
		return nil, err
	}
	if owner.Schema != SchemaVersion || owner.Kind != OwnerKind || !idPattern.MatchString(owner.ID) ||
		owner.UID != os.Geteuid() || owner.Bin != paths.Bin || owner.Store != paths.Store {
		return nil, ErrOwnership
	}
	return &owner, nil
}

func readGeneration(root *os.Root, id string) (Generation, error) {
	return readGenerationForUID(root, id, os.Geteuid())
}

func readGenerationForUID(root *os.Root, id string, uid int) (Generation, error) {
	var generation Generation
	if !idPattern.MatchString(id) {
		return generation, ErrOwnership
	}
	name := filepath.Join(generationDir, id)
	info, err := root.Lstat(name)
	if err != nil {
		return generation, err
	}
	if err := validateReadInfo(info, true, uid); err != nil {
		return generation, err
	}
	if err := readJSONForUID(root, filepath.Join(name, generationFile), &generation, uid); err != nil {
		return generation, err
	}
	if err := generation.Validate(); err != nil {
		return generation, err
	}
	if generation.ID != id {
		return generation, ErrOwnership
	}
	return generation, nil
}

func activeGeneration(root *os.Root) (Generation, error) {
	if _, err := root.Lstat(currentLink); err != nil {
		return Generation{}, err
	}
	target, err := root.Readlink(currentLink)
	if err != nil {
		return Generation{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	id := strings.TrimPrefix(target, generationDir+string(filepath.Separator))
	if target != filepath.Join(generationDir, id) {
		return Generation{}, ErrOwnership
	}
	generation, err := readGeneration(root, id)
	if err != nil {
		return Generation{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return generation, nil
}

func verifyFile(root *os.Root, name string, expected File) error {
	return verifyFileForUID(root, name, expected, os.Geteuid())
}

func verifyFileForUID(root *os.Root, name string, expected File, uid int) error {
	file, err := root.OpenFile(name, readFlags(), 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := validateReadInfo(info, false, uid); err != nil {
		return err
	}
	if info.Size() != expected.Size || info.Mode().Perm() != directoryMode {
		return ErrCorrupt
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, expected.Size+1))
	if err != nil {
		return err
	}
	if n != expected.Size || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return ErrCorrupt
	}
	return nil
}

func verifyGeneration(root *os.Root, generation Generation) error {
	for _, name := range Products() {
		if err := verifyFile(root, filepath.Join(generationDir, generation.ID, name), generation.Files[name]); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrCorrupt, name, err)
		}
	}
	return nil
}
