package benchmarkevidence

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/EpicBlackWolfZ/microfat/internal/releaseworkflow"
)

type Manifest struct {
	EvidenceClass       string           `json:"evidence_class"`
	MeasurementRun      string           `json:"measurement_run_id"`
	MeasurementAttempt  string           `json:"measurement_attempt"`
	VerificationRun     string           `json:"verification_run_id"`
	VerificationAttempt string           `json:"verification_attempt"`
	VerificationSource  string           `json:"verification_source_sha"`
	Limitation          string           `json:"limitation"`
	Bundles             []VerifiedBundle `json:"bundles"`
}

var positiveID = regexp.MustCompile(`^[1-9][0-9]*$`)
var sourceSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func archive(options Options, verified []VerifiedBundle, env Environment) error {
	source, runID, attempt := env("SOURCE_SHA"), env("GITHUB_RUN_ID"), fallback(env("GITHUB_RUN_ATTEMPT"), "1")
	if !sourceSHA.MatchString(source) || !positiveID.MatchString(runID) || !positiveID.MatchString(attempt) {
		return errors.New("archive requires an exact source SHA and verification run/attempt")
	}
	measurementRun, measurementAttempt := measurement(env)
	manifest := Manifest{EvidenceClass: "hosted-comparative", MeasurementRun: measurementRun, MeasurementAttempt: measurementAttempt,
		VerificationRun: runID, VerificationAttempt: attempt, VerificationSource: fallback(env("GITHUB_SHA"), source),
		Limitation: "Shared hosted VMs; no dedicated-hardware performance certification.", Bundles: verified}
	manifestPath := filepath.Join(options.Root, "verified-manifest.json")
	if err := writeJSON(manifestPath, manifest); err != nil {
		return err
	}
	filename := filepath.Join(options.WorkDir, "benchmark-evidence-"+source+"-"+runID+"-"+attempt+".tar.gz")
	if err := writeArchive(filename, manifestPath, verified); err != nil {
		return err
	}
	digest, err := releaseworkflow.HashFile(filename)
	if err != nil {
		return err
	}
	return os.WriteFile(filename+".sha256", fmt.Appendf(nil, "%s  %s\n", digest, filepath.Base(filename)), fileMode)
}

func writeArchive(filename, manifest string, bundles []VerifiedBundle) error {
	output, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode) // #nosec G304 -- selected immutable archive output.
	if err != nil {
		return err
	}
	compressed := gzip.NewWriter(output)
	writer := tar.NewWriter(compressed)
	err = addTree(writer, manifest, "verified-manifest.json")
	for index, bundle := range bundles {
		if err != nil {
			break
		}
		err = addTree(writer, bundle.Bundle, fmt.Sprintf("bundles/%02d", index))
	}
	err = errors.Join(err, writer.Close(), compressed.Close(), output.Close())
	if err != nil {
		return errors.Join(err, os.Remove(filename))
	}
	return nil
}

func addTree(writer *tar.Writer, root, prefix string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	directory, walkRoot := filepath.Dir(root), filepath.Base(root)
	if info.IsDir() {
		directory, walkRoot = root, "."
	}
	scope, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	err = writeTree(writer, scope, walkRoot, prefix)
	return errors.Join(err, scope.Close())
}

func writeTree(writer *tar.Writer, scope *os.Root, root, prefix string) error {
	return fs.WalkDir(scope.FS(), root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("evidence archive accepts only regular files and directories")
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(filepath.Join(prefix, relative))
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		input, err := scope.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, input)
		return errors.Join(copyErr, input.Close())
	})
}
