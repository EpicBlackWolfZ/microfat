//go:build linux

package benchmarkcleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

type tree struct {
	ctx                    context.Context
	env                    environment
	receipt                receipt
	name                   string
	parent, root, registry *os.File
}

func (t *tree) close() {
	for _, file := range []*os.File{t.root, t.parent, t.registry} {
		if file != nil {
			_ = file.Close()
		}
	}
}

func (e environment) open(root, runID, attempt string) (*tree, error) {
	return e.openContext(context.Background(), root, runID, attempt)
}

func (e environment) openContext(ctx context.Context, root, runID, attempt string) (*tree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := e.name(root, runID, attempt)
	if err != nil {
		return nil, err
	}
	t := &tree{ctx: ctx, env: e, name: name}
	opened := false
	defer func() {
		if !opened {
			t.close()
		}
	}()
	t.registry, err = e.registry(false)
	if err != nil {
		return nil, err
	}
	t.receipt, err = e.readReceipt(t.registry, name)
	if err != nil {
		return nil, err
	}
	if t.receipt.Root != root || t.receipt.RunID != runID || t.receipt.Attempt != attempt {
		return nil, errors.New("delegation receipt differs from current run and attempt")
	}
	t.parent, err = openDirectory(e.cgroups)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	t.root, stat, err = e.root(t.parent, name, t.receipt.UID)
	if err != nil {
		return nil, err
	}
	if stat.Dev != t.receipt.Device || stat.Ino != t.receipt.Inode {
		return nil, errors.New("delegation inode differs from registered provenance")
	}
	self, err := e.selfCgroup()
	if err != nil {
		return nil, err
	}
	if err := outside(self, "/"+name); err != nil {
		return nil, err
	}
	// Validate every existing leaf before any signal, including the force fallback.
	if err := t.walk(func(*os.File) error { return nil }, false); err != nil {
		return nil, err
	}
	opened = true
	return t, nil
}

func (e environment) readReceipt(registry *os.File, name string) (receipt, error) {
	file, err := openAt(registry, name+".json", unix.O_RDONLY, 0)
	if err != nil {
		return receipt{}, err
	}
	defer func() { _ = file.Close() }()
	stat, err := statFile(file)
	if err != nil || stat.Uid != e.privilegedUID || stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		stat.Mode&0o777 != receiptMode || stat.Nlink != 1 || stat.Size > maxControlBytes {
		return receipt{}, errors.Join(errors.New("unprotected delegation receipt"), err)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxControlBytes+1))
	decoder.DisallowUnknownFields()
	var value receipt
	if err := decoder.Decode(&value); err != nil {
		return receipt{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return receipt{}, errors.New("trailing delegation receipt data")
	}
	return value, nil
}

func outside(data []byte, root string) error {
	var membership string
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "0::") {
			if membership != "" {
				return errors.New("ambiguous v2 cgroup membership")
			}
			membership = strings.TrimPrefix(line, "0::")
		}
	}
	if membership == "" || !filepath.IsAbs(membership) || membership == root || strings.HasPrefix(membership, root+"/") {
		return errors.New("cleanup must run outside its delegation in a v2 hierarchy")
	}
	return nil
}

func (t *tree) populated() (bool, error) {
	data, err := readAt(t.root, "cgroup.events")
	if err != nil {
		return false, err
	}
	var found bool
	var populated bool
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "populated" {
			if found || len(fields) != 2 || (fields[1] != "0" && fields[1] != "1") {
				return false, errors.New("invalid populated cgroup event")
			}
			found, populated = true, fields[1] == "1"
		}
	}
	if !found {
		return false, errors.New("missing populated cgroup event")
	}
	return populated, nil
}

func pids(file *os.File) ([]int, error) {
	data, err := readAt(file, "cgroup.procs")
	if err != nil {
		return nil, err
	}
	var result []int
	for value := range strings.FieldsSeq(string(data)) {
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 0 {
			return nil, errors.New("invalid delegation PID")
		}
		result = append(result, pid)
	}
	return result, nil
}

func (t *tree) terminate() error {
	return t.walk(func(file *os.File) error {
		members, err := pids(file)
		if err != nil {
			return err
		}
		for _, pid := range members {
			if err := t.signal(file, pid); err != nil {
				return err
			}
		}
		return nil
	}, false)
}

func (t *tree) signal(file *os.File, pid int) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	fd, err := t.env.openPID(pid)
	if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOSYS) {
		// Unsupported pidfds leave termination to the scoped cgroup.kill fallback.
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	// Re-read membership after opening the pidfd. A recycled PID outside this leaf
	// cannot receive a signal; the pidfd keeps the selected process identity pinned.
	members, err := pids(file)
	if err != nil {
		return err
	}
	for _, member := range members {
		if member == pid {
			err = t.env.signalPID(fd)
			if errors.Is(err, unix.ESRCH) {
				return nil
			}
			return err
		}
	}
	return nil
}

func (t *tree) force() error {
	// Revalidate all current descendants: cancellation can interrupt a leaf's creation.
	if err := t.walk(func(*os.File) error { return nil }, false); err != nil {
		return err
	}
	file, err := openAt(t.root, "cgroup.kill", unix.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString("1")
	return errors.Join(writeErr, file.Close())
}

func (t *tree) walk(visit func(*os.File) error, remove bool) error {
	count := 0
	var descend func(*os.File) error
	descend = func(parent *os.File) error {
		if err := t.ctx.Err(); err != nil {
			return err
		}
		count++
		if count > maxGroups {
			return errors.New("delegation exceeds bounded group count")
		}
		if err := visit(parent); err != nil {
			return err
		}
		listing, err := openAt(parent, ".", unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		const maxEntries = maxGroups * 2
		entries, readErr := listing.ReadDir(maxEntries)
		if errors.Is(readErr, io.EOF) {
			readErr = nil
		}
		if err := errors.Join(readErr, listing.Close()); err != nil {
			return err
		}
		if len(entries) == maxEntries {
			return errors.New("delegation exceeds bounded directory entries")
		}
		for _, entry := range entries {
			if err := t.ctx.Err(); err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("symlink in delegation")
			}
			if !entry.IsDir() {
				continue
			}
			if entry.Name() != "manager" && !leafName.MatchString(entry.Name()) {
				return fmt.Errorf("unowned delegation leaf %q", entry.Name())
			}
			if err := t.child(parent, entry.Name(), descend, remove); err != nil {
				return err
			}
		}
		return nil
	}
	return descend(t.root)
}

func (t *tree) child(parent *os.File, name string, descend func(*os.File) error, remove bool) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil // The controller may finish its own cleanup during the walk.
		}
		return err
	}
	if stat.Uid != t.receipt.UID && stat.Uid != t.env.privilegedUID {
		return errors.New("unowned delegation leaf")
	}
	file, opened, err := t.env.root(parent, name, stat.Uid)
	if err != nil {
		return absentLeaf(parent, name, err)
	}
	err = descend(file)
	err = errors.Join(err, file.Close())
	if err != nil {
		return absentLeaf(parent, name, err)
	}
	if !remove {
		return nil
	}
	if err := unix.Fstatat(int(parent.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return absentLeaf(parent, name, err)
	}
	if stat.Dev != opened.Dev || stat.Ino != opened.Ino {
		return errors.New("delegation leaf changed before removal")
	}
	return absentLeaf(parent, name, t.env.unlinkAt(int(parent.Fd()), name, unix.AT_REMOVEDIR))
}

func absentLeaf(parent *os.File, name string, original error) error {
	if !errors.Is(original, unix.ENOENT) {
		return original
	}
	var current unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
		return nil // The owner completed its own removal; missing controls in a live leaf remain fatal.
	}
	return original
}

func (t *tree) remove() error {
	if err := t.walk(func(*os.File) error { return nil }, true); err != nil {
		return err
	}
	var current unix.Stat_t
	if err := unix.Fstatat(int(t.parent.Fd()), t.name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if current.Dev != t.receipt.Device || current.Ino != t.receipt.Inode {
		return errors.New("delegation changed before removal")
	}
	if err := t.env.unlinkAt(int(t.parent.Fd()), t.name, unix.AT_REMOVEDIR); err != nil {
		return err
	}
	return t.env.unlinkAt(int(t.registry.Fd()), t.name+".json", 0)
}
