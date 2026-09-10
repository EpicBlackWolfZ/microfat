//go:build linux

package system

import (
	"errors"

	"golang.org/x/sys/unix"
)

func CheckCgroupRoot(root, version string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(root, &stat); err != nil {
		return err
	}
	want := int64(unix.CGROUP2_SUPER_MAGIC)
	if version == "v1" {
		want = unix.CGROUP_SUPER_MAGIC
	}
	if stat.Type != want {
		return errors.New("configured root is not the requested cgroup filesystem")
	}
	return nil
}
