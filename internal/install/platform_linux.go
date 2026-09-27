package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const lockPoll = 20 * time.Millisecond
const lockTimeout = 10 * time.Second

func readFlags() int  { return os.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK }
func writeFlags() int { return os.O_WRONLY | os.O_CREATE | os.O_EXCL | unix.O_NOFOLLOW }

func validateInfo(info os.FileInfo, directory bool, exactUID bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ErrOwnership
	}
	uid := int(stat.Uid)
	if (exactUID && uid != os.Geteuid()) || (!exactUID && uid != 0 && uid != os.Geteuid()) {
		return ErrOwnership
	}
	if directory {
		if !info.IsDir() {
			return ErrOwnership
		}
		// A root-owned sticky ancestor (such as /tmp) protects owned child names.
		if !exactUID && uid == 0 && info.Mode()&os.ModeSticky != 0 {
			return nil
		}
	} else if !info.Mode().IsRegular() || stat.Nlink != 1 {
		return ErrOwnership
	}
	if info.Mode().Perm()&0o022 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return ErrOwnership
	}
	return nil
}

func lock(ctx context.Context, root *os.Root) (*os.File, error) {
	file, err := root.OpenFile(lockFile, os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW|unix.O_NONBLOCK, metadataMode)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil {
		err = validateInfo(info, false, true)
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	ctx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, file.Close())
		}
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			return nil, errors.Join(err, file.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(fmt.Errorf("waiting for installation lock: %w", ctx.Err()), file.Close())
		case <-time.After(lockPoll):
		}
	}
}
