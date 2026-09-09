//go:build !linux

package cgroup

// RetainedExecutableMemory has no Linux memfd storage to account for on other systems.
func RetainedExecutableMemory() (int64, error) { return 0, nil }
