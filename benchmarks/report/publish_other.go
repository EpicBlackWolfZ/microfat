//go:build !linux

package report

import "errors"

func publish(_, _ string) error {
	return errors.New("atomic no-replace evidence publication requires Linux")
}
