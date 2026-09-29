package releaseworkflow

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/EpicBlackWolfZ/microfat/internal/runtimequalify"
)

const runtimeEvidenceMode = 0o600

func runtimeJob(arch string) string { return "Runtime candidate / Native runtime (" + arch + ")" }

func ValidateRuntimeJobs(jobs []Job) error {
	for _, arch := range []string{"amd64", "arm64"} {
		count := 0
		for _, job := range jobs {
			if job.Name == runtimeJob(arch) {
				count++
				if job.Conclusion != success {
					return errors.New("native runtime qualification did not succeed")
				}
			}
		}
		if count != 1 {
			return errors.New("missing or duplicate native runtime qualification job")
		}
	}
	return nil
}

func ValidateRuntimeSummary(s runtimequalify.Summary, release Release, run WorkflowRun, arch string) error {
	if err := runtimequalify.ValidateSummary(s); err != nil {
		return err
	}
	return validateRuntimeIdentity(s, release, run, arch)
}

func validateRuntimeIdentity(s runtimequalify.Summary, release Release, run WorkflowRun, arch string) error {
	if s.Input != runtimequalify.Candidate || s.Source != run.HeadSHA || s.Tag != release.TagName || s.Architecture != arch ||
		s.RunID != strconv.FormatInt(run.ID, 10) || s.Attempt != strconv.FormatInt(run.Attempt, 10) || s.Release.ID != release.ID {
		return errors.New("runtime qualification belongs to different artifacts, source, architecture or attempt")
	}
	required := map[string]runtimequalify.Asset{}
	for _, asset := range s.Release.Assets {
		if _, ok := s.Assets[asset.Name]; ok || asset.Name == "checksums.txt" || asset.Name == "checksums.txt.sig" {
			required[asset.Name] = asset
		}
	}
	for _, asset := range release.Assets {
		if previous, ok := required[asset.Name]; ok {
			if asset.ID != previous.ID || asset.Size != previous.Size || asset.Digest != previous.Digest || asset.Digest == "" {
				return errors.New("draft assets changed after runtime qualification")
			}
			delete(required, asset.Name)
		}
	}
	if len(required) != 0 {
		return errors.New("qualified draft assets disappeared")
	}
	return nil
}

func sameRuntimeInventory(before, after Release) error {
	if err := errors.Join(ValidateAssets(before), ValidateAssets(after)); err != nil {
		return err
	}
	if before.ID != after.ID || before.TagName != after.TagName {
		return errors.New("draft release identity changed")
	}
	contract, err := releasecheck.NewReleaseContract(strings.TrimPrefix(before.TagName, "v"))
	if err != nil {
		return err
	}
	wanted := map[string]Asset{}
	for _, a := range before.Assets {
		if _, ok := contract.ExpectedPayloadNames[a.Name]; ok || a.Name == "checksums.txt" || a.Name == "checksums.txt.sig" {
			wanted[a.Name] = a
		}
	}
	for _, a := range after.Assets {
		if old, ok := wanted[a.Name]; ok {
			if old != a || a.Digest == "" || a.ID <= 0 {
				return errors.New("draft inventory changed before publication")
			}
			delete(wanted, a.Name)
		}
	}
	if len(wanted) > 0 {
		return errors.New("draft inventory incomplete before publication")
	}
	return nil
}

func qualifyRuntimeRelease(repo, tag, source string, runs []WorkflowRun, release Release, gh Command) ([]string, error) {
	if !releasecheck.UsesInstallerHelper(strings.TrimPrefix(tag, "v")) {
		return nil, nil
	}
	var producer WorkflowRun
	for _, run := range runs {
		if run.HeadSHA == source && run.HeadBranch == tag && run.Event == "push" && run.Path == ReleaseWorkflow {
			producer = run
		}
	}
	if producer.ID <= 0 || producer.Attempt <= 0 {
		return nil, errors.New("missing exact runtime producer attempt")
	}
	var jobs struct {
		Jobs  []Job `json:"jobs"`
		Total int   `json:"total_count"`
	}
	endpoint := fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d/jobs?per_page=100", repo, producer.ID, producer.Attempt)
	if err := commandJSON(gh, &jobs, "api", endpoint); err != nil {
		return nil, err
	}
	if jobs.Total > len(jobs.Jobs) {
		return nil, errors.New("incomplete producer job inventory")
	}
	if err := ValidateRuntimeJobs(jobs.Jobs); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "microfat-runtime-evidence-")
	if err != nil {
		return nil, err
	}
	// Returned files must survive until the finalizer uploads them. The caller's
	// workflow owns this temporary directory; no caller-supplied directory is removed.
	for _, arch := range []string{"amd64", "arm64"} {
		target := filepath.Join(directory, arch)
		name := fmt.Sprintf("runtime-candidate-%s-%d-%d", arch, producer.ID, producer.Attempt)
		if _, err := gh("run", "download", strconv.FormatInt(producer.ID, 10), "--repo", repo, "--name", name, "--dir", target); err != nil {
			return nil, err
		}
		summary, err := readRuntimeSummary(target)
		if err != nil {
			return nil, err
		}
		if err := ValidateRuntimeSummary(summary, release, producer, arch); err != nil {
			return nil, err
		}
	}
	return packageRuntimeEvidence(directory, tag)
}

func packageRuntimeEvidence(directory, tag string) ([]string, error) {
	archive := filepath.Join(directory, "runtime-evidence_"+strings.TrimPrefix(tag, "v")+".tar.gz")
	if err := archiveRuntimeEvidence(directory, archive); err != nil {
		return nil, err
	}
	hash, err := HashFile(archive)
	if err != nil {
		return nil, err
	}
	checksum := archive + ".sha256"
	if err := os.WriteFile(checksum, []byte(hash+"  "+filepath.Base(archive)+"\n"), runtimeEvidenceMode); err != nil {
		return nil, err
	}
	return []string{archive, checksum}, nil
}

func readRuntimeSummary(root string) (runtimequalify.Summary, error) {
	var result runtimequalify.Summary
	var names []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in runtime evidence")
		}
		if entry.Name() == "summary.json" && !entry.IsDir() {
			names = append(names, path)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if len(names) != 1 {
		return result, errors.New("expected one complete runtime summary")
	}
	file, err := os.Open(names[0])
	if err != nil {
		return result, err
	}
	defer func() { _ = file.Close() }()
	const summaryLimit = 64 << 20
	decoder := json.NewDecoder(io.LimitReader(file, summaryLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return result, errors.New("trailing runtime evidence")
	}
	return result, nil
}

func archiveRuntimeEvidence(root, destination string) error {
	// #nosec G304 -- fixed archive filename within this finalizer's private temporary directory.
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, runtimeEvidenceMode)
	if err != nil {
		return err
	}
	compressed := gzip.NewWriter(file)
	writer := tar.NewWriter(compressed)
	directory, openErr := os.OpenRoot(root)
	if openErr != nil {
		return errors.Join(openErr, writer.Close(), compressed.Close(), file.Close())
	}
	defer func() { _ = directory.Close() }()
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path == destination {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("non-regular runtime evidence")
		}
		name, err := filepath.Rel(root, path)
		if err != nil || !filepath.IsLocal(name) {
			return errors.New("runtime evidence outside private root")
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(name)
		header.ModTime, header.AccessTime, header.ChangeTime = time.Unix(0, 0), time.Time{}, time.Time{}
		header.Uid, header.Gid, header.Uname, header.Gname = 0, 0, "", ""
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		input, err := directory.Open(name)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, input)
		return errors.Join(copyErr, input.Close())
	})
	return errors.Join(err, writer.Close(), compressed.Close(), file.Close())
}
