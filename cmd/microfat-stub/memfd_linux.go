//go:build linux

package main

import (
	"github.com/EpicBlackWolfZ/microfat/internal/memfd"
)

func createExecutableMemfd() (int, error) {
	adapter := &memfd.SyscallAdapter{
		MemfdCreate: memfdCreateFunc,
	}
	res, err := memfd.CreateExecutable("microfat_payload", adapter)
	if err != nil {
		return -1, err
	}
	return res.FD, nil
}
