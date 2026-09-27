package install

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Uninstall removes only unchanged owned entrypoint links. It intentionally keeps
// generations and the stable lock so delayed readers and future reinstalls remain safe.
func Uninstall(ctx context.Context, snapshot Snapshot) (Result, error) {
	result := Result{}
	if snapshot.owner == nil {
		return result, ErrOwnership
	}
	op, err := openOperation(ctx, snapshot)
	if err != nil {
		return result, err
	}
	defer op.close()
	var failures []error
	for _, name := range Products() {
		target, err := op.bin.Readlink(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || target != op.linkTarget(name) {
			result.Preserved = append(result.Preserved, name)
			failures = append(failures, fmt.Errorf("%w: preserved %s", ErrConflict, name))
			continue
		}
		if err := op.bin.Remove(name); err != nil {
			failures = append(failures, err)
			continue
		}
		result.Removed = append(result.Removed, name)
	}
	failures = append(failures, syncDir(op.bin, "."))
	return result, errors.Join(failures...)
}
