//go:build !unix

package report

import (
	"errors"
	"os"
)

func openEvidence(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("non-regular evidence entry")
	}
	return os.Open(path)
}
