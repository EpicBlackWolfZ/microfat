package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Inspect captures ownership before any download or installation mutation.
func Inspect(paths Paths) (Snapshot, error) {
	snapshot := Snapshot{paths: paths}
	if runtime.GOOS != "linux" {
		return snapshot, ErrUnsupported
	}
	if err := paths.Validate(); err != nil {
		return snapshot, err
	}
	for _, name := range []string{paths.Store, paths.Bin} {
		if err := validateAncestors(name); err != nil {
			return snapshot, err
		}
	}
	var err error
	snapshot.binInfo, err = rootInfo(paths.Bin)
	if err != nil {
		return snapshot, err
	}
	snapshot.storeInfo, err = rootInfo(paths.Store)
	if err != nil || snapshot.storeInfo == nil {
		return snapshot, err
	}
	root, err := os.OpenRoot(paths.Store)
	if err != nil {
		return snapshot, err
	}
	defer func() { _ = root.Close() }()
	snapshot.owner, err = readOwner(root, paths)
	if errors.Is(err, os.ErrNotExist) {
		err = validateUnclaimed(root)
	}
	return snapshot, err
}

func validateUnclaimed(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	// A new store can contain only its stable lock and interrupted owner metadata.
	for {
		entries, err := file.ReadDir(1)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		entry := entries[0]
		if entry.Name() != lockFile && !strings.HasPrefix(entry.Name(), ".owner-") {
			return ErrOwnership
		}
		info, err := root.Lstat(entry.Name())
		if err != nil {
			return err
		}
		if err := validateInfo(info, false, true); err != nil {
			return err
		}
	}
}

type operation struct {
	store     *os.Root
	bin       *os.Root
	lock      *os.File
	paths     Paths
	storeInfo os.FileInfo
	binInfo   os.FileInfo
	hook      func(string) error
}

func (op *operation) close() {
	if op.bin != nil {
		_ = op.bin.Close()
	}
	if op.lock != nil {
		_ = op.lock.Close()
	}
	if op.store != nil {
		_ = op.store.Close()
	}
}

func openOperation(ctx context.Context, snapshot Snapshot) (*operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	op := &operation{paths: snapshot.paths, hook: func(string) error { return nil }}
	if err := snapshot.paths.Validate(); err != nil {
		return nil, err
	}
	for _, name := range []string{snapshot.paths.Store, snapshot.paths.Bin} {
		if err := validateAncestors(name); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(name, directoryMode); err != nil {
			return nil, err
		}
	}
	var err error
	op.storeInfo, err = rootInfo(snapshot.paths.Store)
	if err != nil {
		return nil, err
	}
	op.binInfo, err = rootInfo(snapshot.paths.Bin)
	if err != nil {
		return nil, err
	}
	if (snapshot.storeInfo != nil && !os.SameFile(snapshot.storeInfo, op.storeInfo)) ||
		(snapshot.binInfo != nil && !os.SameFile(snapshot.binInfo, op.binInfo)) {
		return nil, ErrChanged
	}
	op.store, err = os.OpenRoot(snapshot.paths.Store)
	if err != nil {
		return nil, err
	}
	op.lock, err = lock(ctx, op.store)
	if err == nil {
		op.bin, err = os.OpenRoot(snapshot.paths.Bin)
	}
	if err == nil {
		err = op.checkRoots()
	}
	if err == nil {
		err = op.checkOwner(snapshot.owner)
	}
	if err != nil {
		op.close()
		return nil, err
	}
	return op, nil
}

func (op *operation) checkRoots() error {
	for _, pair := range []struct {
		root *os.Root
		path string
		info os.FileInfo
	}{
		{op.store, op.paths.Store, op.storeInfo}, {op.bin, op.paths.Bin, op.binInfo},
	} {
		current, err := rootInfo(pair.path)
		if err != nil {
			return err
		}
		opened, err := pair.root.Stat(".")
		if err != nil {
			return err
		}
		if current == nil || !os.SameFile(current, pair.info) || !os.SameFile(opened, pair.info) {
			return ErrChanged
		}
	}
	return nil
}

func (op *operation) checkOwner(expected *Owner) error {
	actual, err := readOwner(op.store, op.paths)
	if expected != nil {
		if err != nil {
			return fmt.Errorf("%w: %w", ErrChanged, err)
		}
		if *expected != *actual {
			return ErrChanged
		}
		return nil
	}
	if err == nil {
		return ErrChanged
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return validateUnclaimed(op.store)
}

func (op *operation) linkTarget(name string) string {
	return filepath.Join(op.paths.Store, currentLink, name)
}

func (op *operation) checkEntrypoints(owned bool) error {
	for _, name := range Products() {
		info, err := op.bin.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !owned || info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%w: %s", ErrConflict, name)
		}
		target, err := op.bin.Readlink(name)
		if err != nil {
			return err
		}
		if target != op.linkTarget(name) {
			return fmt.Errorf("%w: %s", ErrConflict, name)
		}
	}
	return nil
}

func (op *operation) initializeOwner() error {
	owner := Owner{Schema: SchemaVersion, Kind: OwnerKind, ID: NewID(), UID: os.Geteuid(), Bin: op.paths.Bin, Store: op.paths.Store}
	temporary := ".owner-" + NewID()
	if err := writeJSON(op.store, temporary, owner); err != nil {
		return err
	}
	if err := op.store.Rename(temporary, ownerFile); err != nil {
		return err
	}
	return syncDir(op.store, ".")
}

// Apply copies already authenticated product bytes into a complete generation and activates it.
// A nonzero Result.Activated with an error means activation happened but a later operation failed.
func Apply(ctx context.Context, snapshot Snapshot, generation Generation, source string, opts ApplyOptions) (Result, error) {
	return apply(ctx, snapshot, generation, source, opts, nil)
}

func apply(ctx context.Context, snapshot Snapshot, generation Generation, source string,
	opts ApplyOptions, hook func(string) error,
) (Result, error) {
	result := Result{Generation: generation}
	if err := generation.Validate(); err != nil {
		return result, err
	}
	op, err := openOperation(ctx, snapshot)
	if err != nil {
		return result, err
	}
	defer op.close()
	if hook != nil {
		op.hook = hook
	}
	if err := op.checkEntrypoints(snapshot.owner != nil); err != nil {
		return result, err
	}
	if snapshot.owner == nil {
		if err := op.initializeOwner(); err != nil {
			return result, err
		}
	}
	if err := op.cleanupStaging(); err != nil {
		return result, err
	}
	current, err := op.reusableGeneration(generation, opts)
	if err != nil {
		return result, err
	}
	if current != nil {
		result.Generation, result.Reused = *current, true
		return result, op.publishEntrypoints()
	}
	if err := op.stage(source, generation); err != nil {
		return result, err
	}
	if err := op.hook("generation-published"); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := op.checkRoots(); err != nil {
		return result, err
	}
	if err := op.checkEntrypoints(true); err != nil {
		return result, err
	}
	if err := op.hook("before-activation"); err != nil {
		return result, err
	}
	temporary := ".current-" + NewID()
	if err := op.store.Symlink(filepath.Join(generationDir, generation.ID), temporary); err != nil {
		return result, err
	}
	defer func() { _ = op.store.Remove(temporary) }()
	if err := op.store.Rename(temporary, currentLink); err != nil {
		return result, err
	}
	result.Activated = true
	if err := op.hook("activated"); err != nil {
		return result, err
	}
	if err := syncDir(op.store, "."); err != nil {
		return result, fmt.Errorf("activated; durability not confirmed: %w", err)
	}
	return result, op.publishEntrypoints()
}

func (op *operation) reusableGeneration(requested Generation, opts ApplyOptions) (*Generation, error) {
	current, err := activeGeneration(op.store)
	if errors.Is(err, os.ErrNotExist) || (opts.Repair && errors.Is(err, ErrCorrupt)) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := verifyGeneration(op.store, current); err != nil {
		if opts.Repair {
			return nil, nil
		}
		return nil, err
	}
	if sameRelease(current, requested) {
		return &current, nil
	}
	return nil, nil
}

func (op *operation) cleanupStaging() error {
	file, err := op.store.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	const maxEntries = 1024
	entries, err := file.ReadDir(maxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > maxEntries {
		return errors.New("installation root has too many entries")
	}
	for _, entry := range entries {
		name := entry.Name()
		prefix, directory := ".stage-", true
		if strings.HasPrefix(name, ".owner-") {
			prefix, directory = ".owner-", false
		}
		if !strings.HasPrefix(name, prefix) || !idPattern.MatchString(strings.TrimPrefix(name, prefix)) {
			continue
		}
		info, err := op.store.Lstat(name)
		if err != nil {
			return err
		}
		if err := validateInfo(info, directory, true); err != nil {
			return fmt.Errorf("unsafe interrupted staging: %w", err)
		}
		if err := op.store.RemoveAll(name); err != nil {
			return err
		}
	}
	return nil
}

func sameRelease(a, b Generation) bool {
	return a.Version == b.Version && a.Arch == b.Arch && a.ArchiveSHA256 == b.ArchiveSHA256 && maps.Equal(a.Files, b.Files)
}

func (op *operation) stage(source string, generation Generation) error {
	if err := op.store.MkdirAll(generationDir, directoryMode); err != nil {
		return err
	}
	info, err := op.store.Lstat(generationDir)
	if err != nil {
		return err
	}
	if err := validateInfo(info, true, true); err != nil {
		return err
	}
	temporary := ".stage-" + NewID()
	if err := op.store.Mkdir(temporary, privateMode); err != nil {
		return err
	}
	defer func() { _ = op.store.RemoveAll(temporary) }()
	input, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	for _, name := range Products() {
		if err := copyProduct(input, op.store, name, filepath.Join(temporary, name), generation.Files[name]); err != nil {
			return err
		}
		if err := op.hook("staged-" + name); err != nil {
			return err
		}
	}
	if err := writeJSON(op.store, filepath.Join(temporary, generationFile), generation); err != nil {
		return err
	}
	if err := publishableDirectory(op.store, temporary); err != nil {
		return err
	}
	// A generation is never replaced, including during explicit repair.
	target := filepath.Join(generationDir, generation.ID)
	if _, err := op.store.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("generation ID already exists")
	}
	if err := op.store.Rename(temporary, target); err != nil {
		return err
	}
	return syncDir(op.store, generationDir)
}

func copyProduct(source, destination *os.Root, name, target string, expected File) error {
	input, err := source.OpenFile(name, readFlags(), 0)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if err := validateInfo(info, false, true); err != nil {
		return err
	}
	if info.Size() != expected.Size {
		return ErrCorrupt
	}
	output, err := destination.OpenFile(target, writeFlags(), metadataMode)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, expected.Size+1))
	if copyErr == nil && (n != expected.Size || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256) {
		copyErr = ErrCorrupt
	}
	if copyErr == nil {
		copyErr = output.Chmod(directoryMode)
	}
	return errors.Join(copyErr, output.Sync(), output.Close())
}

func (op *operation) publishEntrypoints() error {
	for _, name := range Products() {
		err := op.bin.Symlink(op.linkTarget(name), name)
		if errors.Is(err, os.ErrExist) {
			err = op.checkEntrypoints(true)
		}
		if err != nil {
			return err
		}
		if err := syncDir(op.bin, "."); err != nil {
			return err
		}
		if err := op.hook("linked-" + name); err != nil {
			return err
		}
	}
	return nil
}
