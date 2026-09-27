//go:build !linux

package install

import (
	"context"
	"os"
)

func readFlags() int                                   { return os.O_RDONLY }
func fileUID(os.FileInfo) int                          { return -1 }
func writeFlags() int                                  { return os.O_WRONLY | os.O_CREATE | os.O_EXCL }
func validateInfo(os.FileInfo, bool, bool) error       { return ErrUnsupported }
func lock(context.Context, *os.Root) (*os.File, error) { return nil, ErrUnsupported }
