//go:build !linux

package system

import "errors"

func CheckCgroupRoot(_, _ string) error {
	return errors.New("cgroups require Linux")
}
