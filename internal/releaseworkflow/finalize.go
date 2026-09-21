package releaseworkflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const releaseCommand = "release"

type Command func(...string) ([]byte, error)
type Environment func(string) string

type Asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type Release struct {
	Draft     bool    `json:"draft"`
	Immutable bool    `json:"immutable"`
	TagName   string  `json:"tag_name"`
	Assets    []Asset `json:"assets"`
}

func ValidateAssets(release Release) error {
	if !release.Draft || release.Immutable {
		return errors.New("release must be an unpublished mutable draft")
	}
	names := make(map[string]bool)
	for _, asset := range release.Assets {
		if asset.Name == "" || asset.Size <= 0 || names[asset.Name] {
			return errors.New("missing, empty or duplicate release asset")
		}
		names[asset.Name] = true
	}
	required := []string{"checksums.txt", "checksums.txt.sig"}
	version := strings.TrimPrefix(release.TagName, "v")
	for _, arch := range []string{"amd64", "arm64"} {
		archive := fmt.Sprintf("microfat_%s_linux_%s.tar.gz", version, arch)
		for _, suffix := range []string{"", ".spdx.json", ".cyclonedx.json"} {
			required = append(required, archive+suffix)
		}
	}
	for _, name := range required {
		if !names[name] {
			return fmt.Errorf("missing release archive, SBOM or signature: %s", name)
		}
	}
	return nil
}

func commandJSON(gh Command, target any, args ...string) error {
	data, err := gh(args...)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func RecoverySource(repo, runID, attempt string, env Environment, gh Command) (string, string, error) {
	if env("GITHUB_EVENT_NAME") != "workflow_dispatch" || env("GITHUB_REF") != "refs/heads/main" {
		return "", "", errors.New("recovery requires explicit dispatch from trusted main")
	}
	if !repositoryName.MatchString(repo) || !positiveID.MatchString(runID) || !positiveID.MatchString(attempt) {
		return "", "", errors.New("invalid repository, measurement run or attempt")
	}
	endpoint := fmt.Sprintf("repos/%s/actions/runs/%s/attempts/%s", repo, runID, attempt)
	var run WorkflowRun
	if err := commandJSON(gh, &run, "api", endpoint); err != nil {
		return "", "", err
	}
	var response struct {
		Jobs []Job `json:"jobs"`
	}
	if err := commandJSON(gh, &response, "api", endpoint+"/jobs?per_page=100"); err != nil {
		return "", "", err
	}
	source, err := SourceRevision(run, response.Jobs, runID, attempt)
	if err != nil {
		return "", "", err
	}
	verified := 0
	for _, job := range response.Jobs {
		if job.Name != VerificationJob {
			continue
		}
		if job.Conclusion != success {
			return "", "", errors.New("independent archive verification must have succeeded")
		}
		verified++
	}
	if verified != 1 {
		return "", "", errors.New("expected exactly one successful independent archive verification job")
	}
	if !releaseTag.MatchString(run.HeadBranch) {
		return "", "", errors.New("recovery source must be a release tag")
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := commandJSON(gh, &commit, "api", "repos/"+repo+"/commits/"+run.HeadBranch); err != nil {
		return "", "", err
	}
	if commit.SHA != source {
		return "", "", errors.New("tag differs from verified measurement source")
	}
	return run.HeadBranch, source, nil
}

type FinalizeOptions struct{ Root, RecoveryRun, RecoveryAttempt string }

func Finalize(options FinalizeOptions, env Environment, gh Command) error {
	repo := env("GH_REPO")
	if !repositoryName.MatchString(repo) {
		return errors.New("invalid release repository")
	}
	tag, source := env("RELEASE_TAG"), env("GITHUB_SHA")
	if options.RecoveryRun != "" {
		var err error
		tag, source, err = RecoverySource(repo, options.RecoveryRun, options.RecoveryAttempt, env, gh)
		if err != nil {
			return err
		}
	} else if env("GITHUB_EVENT_NAME") != "push" || env("GITHUB_REF") != "refs/tags/"+tag {
		return errors.New("publication requires a tag push")
	}
	if !releaseTag.MatchString(tag) || !sourceSHA.MatchString(source) {
		return errors.New("invalid release tag or source revision")
	}
	var runs struct {
		Runs []WorkflowRun `json:"workflow_runs"`
	}
	endpoint := fmt.Sprintf("repos/%s/actions/workflows/release.yml/runs?event=push&head_sha=%s&per_page=100", repo, source)
	if err := commandJSON(gh, &runs, "api", endpoint); err != nil {
		return err
	}
	if err := ValidateBuild(runs.Runs, tag, source); err != nil {
		return err
	}
	release, err := draftRelease(repo, tag, gh)
	if err != nil {
		return err
	}
	archive, checksum, err := evidenceFiles(options.Root)
	if err != nil {
		return err
	}
	for _, file := range []string{archive, checksum} {
		if err := uploadEvidence(release, file, gh); err != nil {
			return err
		}
	}
	_, err = gh(releaseCommand, "edit", tag, "--draft=false", "--latest")
	return err
}

func draftRelease(repo, tag string, gh Command) (Release, error) {
	id, err := gh(releaseCommand, "view", tag, "--json", "databaseId", "--jq", ".databaseId")
	if err != nil {
		return Release{}, err
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(id)), 10, 64)
	if err != nil || value <= 0 {
		return Release{}, errors.New("invalid release database ID")
	}
	var release Release
	if err := commandJSON(gh, &release, "api", fmt.Sprintf("repos/%s/releases/%d", repo, value)); err != nil {
		return release, err
	}
	if release.TagName != tag {
		return release, errors.New("release tag mismatch")
	}
	return release, ValidateAssets(release)
}

func HashFile(file string) (string, error) {
	input, err := os.Open(file) // #nosec G304 -- operator-selected evidence artifact.
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, input)
	if err := errors.Join(readErr, input.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func evidenceFiles(root string) (string, string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", "", err
	}
	var archives []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tar.gz") {
			archives = append(archives, filepath.Join(root, entry.Name()))
		}
	}
	if len(archives) != 1 {
		return "", "", errors.New("expected exactly one verified evidence archive")
	}
	archive := archives[0]
	digest, err := HashFile(archive)
	if err != nil {
		return "", "", err
	}
	checksum := archive + ".sha256"
	data, err := os.ReadFile(checksum) // #nosec G304 -- checksum accompanying the selected evidence archive.
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(string(data))
	const expectedFields = 2
	if len(fields) != expectedFields || fields[0] != digest || fields[1] != filepath.Base(archive) {
		return "", "", errors.New("evidence checksum mismatch")
	}
	return archive, checksum, nil
}

func uploadEvidence(release Release, file string, gh Command) error {
	for _, asset := range release.Assets {
		if asset.Name != filepath.Base(file) {
			continue
		}
		digest, err := HashFile(file)
		if err != nil {
			return err
		}
		if asset.Digest != "sha256:"+digest {
			return errors.New("existing evidence asset differs or has no verifiable digest")
		}
		return nil
	}
	_, err := gh(releaseCommand, "upload", release.TagName, file)
	return err
}
