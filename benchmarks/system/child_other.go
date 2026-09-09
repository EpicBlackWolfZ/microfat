//go:build !linux

package system

import "errors"

func SupportedAffinity(_ []int) ([]int, error) { return nil, errors.New("Linux affinity unavailable") }
func ExecChild(_ ChildConfig) error            { return errors.New("benchmark process controls require Linux") }
