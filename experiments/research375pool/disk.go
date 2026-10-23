package main

import (
	"errors"
	"fmt"
	"math"

	"golang.org/x/sys/unix"
)

const minimumStudyFreeBytes = uint64(2 << 30)

type diskProbe func(string) (uint64, error)

type studyDiskCheck struct {
	Sequence       int    `json:"before_trial_sequence"`
	AvailableBytes uint64 `json:"available_bytes"`
	MinimumBytes   uint64 `json:"minimum_bytes"`
	Error          string `json:"error,omitempty"`
}

func checkStudyDisk(root string, sequence int, probe diskProbe) (studyDiskCheck, error) {
	check := studyDiskCheck{Sequence: sequence, MinimumBytes: minimumStudyFreeBytes}
	if probe == nil {
		probe = studyFreeBytes
	}
	available, err := probe(root)
	check.AvailableBytes = available
	if err == nil && available < minimumStudyFreeBytes {
		err = fmt.Errorf("study disk guard: available %d bytes below %d", available, minimumStudyFreeBytes)
	}
	if err != nil {
		check.Error = err.Error()
	}
	return check, err
}

func studyFreeBytes(path string) (uint64, error) {
	var info unix.Statfs_t
	if err := unix.Statfs(path, &info); err != nil {
		return 0, err
	}
	if info.Bsize <= 0 {
		return 0, errors.New("invalid filesystem block size")
	}
	blockSize := uint64(info.Bsize) // #nosec G115 -- positive block size checked above.
	if info.Bavail > math.MaxUint64/blockSize {
		return 0, errors.New("filesystem free-byte count overflow")
	}
	return info.Bavail * blockSize, nil
}
