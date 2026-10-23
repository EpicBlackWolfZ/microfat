package main

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Every attempt is recorded and fsynced before deleting a successful artifact.
// Block zero retains one artifact per family/profile/control/arm. Failures keep
// their previous output or any failed partial evidence; no failure is discarded.
func retainStudyObservation(root string, sequence int, o *observation) error {
	remove := summarySucceeded(*o) && o.Spec.Block > 0
	o.ArtifactRetention = "retained-first-block-or-failure"
	if remove {
		o.ArtifactRetention = "delete-after-durable-record"
	}
	dir := filepath.Join(root, fmt.Sprintf("trial-%03d", sequence))
	if o.Request.Output != filepath.Join(dir, "artifact") {
		return errors.New("refusing retention cleanup outside the owned trial artifact")
	}
	if err := writeDurableStudyJSON(filepath.Join(dir, "observation.json"), o); err != nil {
		return err
	}
	if remove {
		if err := os.Remove(o.Request.Output); err != nil {
			return fmt.Errorf("removing verified owned artifact after durable record: %w", err)
		}
		o.ArtifactRetention = "removed-after-durable-record"
	}
	return nil
}

func writeDurableStudyJSON(path string, value any) (returnErr error) {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, privateFileMode)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, f.Close()) }()
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
