//go:build !minimal

package main

import (
	"errors"
	"fmt"
	"os"
)

var errDeploymentChanged = errors.New("deployment pathname no longer identifies the running image; " +
	"restart from the current deployment before transforming it")

func validateDeploymentPath(path string, image *os.File) error {
	if image == nil || path == "" {
		return errDeploymentChanged
	}
	running, err := image.Stat()
	if err != nil {
		return fmt.Errorf("%w: %w", errDeploymentChanged, err)
	}
	current, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %w", errDeploymentChanged, err)
	}
	if !os.SameFile(running, current) {
		return errDeploymentChanged
	}
	return nil
}
