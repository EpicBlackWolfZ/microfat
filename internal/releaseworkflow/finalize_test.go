package releaseworkflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func releaseAssets() []Asset {
	names := []string{"checksums.txt", "checksums.txt.sig", "microfat_0.2.5_linux_amd64.tar.gz",
		"microfat_0.2.5_linux_amd64.tar.gz.spdx.json", "microfat_0.2.5_linux_amd64.tar.gz.cyclonedx.json",
		"microfat_0.2.5_linux_arm64.tar.gz", "microfat_0.2.5_linux_arm64.tar.gz.spdx.json",
		"microfat_0.2.5_linux_arm64.tar.gz.cyclonedx.json"}
	var assets []Asset
	for _, name := range names {
		assets = append(assets, Asset{Name: name, Size: 1})
	}
	return assets
}

func TestRequiredDraftAssets(t *testing.T) {
	t.Parallel()
	release := Release{Draft: true, TagName: fixtureTag, Assets: releaseAssets()}
	require.NoError(t, ValidateAssets(release))
	for index := range release.Assets {
		changed := release
		changed.Assets = slices.Delete(slices.Clone(release.Assets), index, index+1)
		require.Error(t, ValidateAssets(changed))
	}
	for _, change := range []func(*Release){func(r *Release) { r.Draft = false }, func(r *Release) { r.Immutable = true },
		func(r *Release) { r.Assets[0].Size = 0 }, func(r *Release) { r.Assets[0].Name = "" },
		func(r *Release) { r.Assets = append(r.Assets, r.Assets[0]) }} {
		changed := release
		changed.Assets = slices.Clone(release.Assets)
		change(&changed)
		require.Error(t, ValidateAssets(changed))
	}
}

type publicationFixture struct {
	t             *testing.T
	root, archive string
	env           map[string]string
	run           WorkflowRun
	release       Release
	calls         [][]string
	failAt        int
}

func newPublication(t *testing.T) *publicationFixture {
	t.Helper()
	f := &publicationFixture{t: t, root: t.TempDir(), run: successfulBuild(),
		release: Release{Draft: true, TagName: fixtureTag, Assets: releaseAssets()},
		env: map[string]string{"GH_REPO": fixtureRepo, "RELEASE_TAG": fixtureTag, "GITHUB_SHA": fixtureSHA,
			"GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/tags/" + fixtureTag}}
	f.archive = filepath.Join(f.root, "evidence.tar.gz")
	require.NoError(t, os.WriteFile(f.archive, []byte("fixture"), 0o600))
	digest, err := HashFile(f.archive)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.archive+".sha256", fmt.Appendf(nil, "%s  evidence.tar.gz\n", digest), 0o600))
	return f
}

func (f *publicationFixture) command(args ...string) ([]byte, error) {
	f.calls = append(f.calls, slices.Clone(args))
	if f.failAt == len(f.calls) {
		return nil, errors.New("GitHub operation failed")
	}
	if args[0] == "api" {
		switch {
		case strings.Contains(args[1], "/attempts/") || strings.Contains(args[1], "/commits/"):
			jobs := append(completeJobs(), Job{Name: VerificationJob, Conclusion: success})
			return recoveryCommand(f.t, measurementRun(), jobs, fixtureSHA)(args...)
		case strings.Contains(args[1], "/actions/"):
			return jsonBytes(f.t, map[string]any{"workflow_runs": []WorkflowRun{f.run}}), nil
		default:
			return jsonBytes(f.t, f.release), nil
		}
	}
	if args[1] == "view" {
		return []byte("123\n"), nil
	}
	require.NotContains(f.t, args, "--clobber", "existing release assets must never be replaced")
	return nil, nil
}

func (f *publicationFixture) finalize() error {
	return Finalize(FinalizeOptions{Root: f.root}, lookup(f.env), f.command)
}
func (f *publicationFixture) publishCount() int {
	count := 0
	for _, args := range f.calls {
		if args[0] == releaseCommand && args[1] == "edit" {
			count++
		}
	}
	return count
}

func TestPublicationOrderAndRecovery(t *testing.T) {
	t.Parallel()
	f := newPublication(t)
	require.NoError(t, f.finalize())
	require.Len(t, f.calls, 6)
	require.Equal(t, []string{releaseCommand, "upload", fixtureTag, f.archive}, f.calls[3])
	require.Equal(t, []string{releaseCommand, "upload", fixtureTag, f.archive + ".sha256"}, f.calls[4])
	require.Equal(t, []string{releaseCommand, "edit", fixtureTag, "--draft=false", "--latest"}, f.calls[5])
	f = newPublication(t)
	f.env["GITHUB_EVENT_NAME"] = "workflow_dispatch"
	f.env["GITHUB_REF"] = "refs/heads/main"
	require.NoError(t, Finalize(FinalizeOptions{Root: f.root, RecoveryRun: "1", RecoveryAttempt: "2"}, lookup(f.env), f.command))
	require.Equal(t, 1, f.publishCount())
	f = newPublication(t)
	require.Error(t, Finalize(FinalizeOptions{Root: f.root, RecoveryRun: "1", RecoveryAttempt: "2"}, lookup(f.env), f.command))
	require.Empty(t, f.calls, "recovery from a tag push must fail before any GitHub command")
}

func TestPublicationRejectsIncompleteInputs(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*publicationFixture){
		"repository": func(f *publicationFixture) { f.env["GH_REPO"] = "invalid" },
		"event":      func(f *publicationFixture) { f.env["GITHUB_EVENT_NAME"] = "pull_request" },
		"ref":        func(f *publicationFixture) { f.env["GITHUB_REF"] = "refs/heads/main" },
		"tag": func(f *publicationFixture) {
			f.env["RELEASE_TAG"] = invalidValue
			f.env["GITHUB_REF"] = "refs/tags/bad"
		},
		"source":             func(f *publicationFixture) { f.env["GITHUB_SHA"] = invalidValue },
		"failed build":       func(f *publicationFixture) { f.run.Conclusion = failedConclusion },
		"running build":      func(f *publicationFixture) { f.run.Status = "in_progress" },
		"wrong build source": func(f *publicationFixture) { f.run.HeadSHA = "wrong" },
		"wrong build tag":    func(f *publicationFixture) { f.run.HeadBranch = "v0.2.4" },
		"published":          func(f *publicationFixture) { f.release.Draft = false },
		"immutable":          func(f *publicationFixture) { f.release.Immutable = true },
		"wrong release tag":  func(f *publicationFixture) { f.release.TagName = "v0.2.4" },
		"missing SBOM":       func(f *publicationFixture) { f.release.Assets = f.release.Assets[:len(f.release.Assets)-1] },
		"empty signature":    func(f *publicationFixture) { f.release.Assets[1].Size = 0 },
		"changed archive":    func(f *publicationFixture) { require.NoError(f.t, os.WriteFile(f.archive, []byte("changed"), 0o600)) },
		"missing root":       func(f *publicationFixture) { f.root = filepath.Join(f.root, "missing") },
		"existing conflicting asset": func(f *publicationFixture) {
			f.release.Assets = append(f.release.Assets, Asset{Name: "evidence.tar.gz", Size: 1, Digest: "sha256:wrong"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newPublication(t)
			change(f)
			require.Error(t, f.finalize())
			require.Zero(t, f.publishCount())
		})
	}
	for failAt := 1; failAt <= 6; failAt++ {
		t.Run(fmt.Sprintf("GitHub operation %d", failAt), func(t *testing.T) {
			t.Parallel()
			f := newPublication(t)
			f.failAt = failAt
			require.ErrorContains(t, f.finalize(), "GitHub operation failed")
			require.Len(t, f.calls, failAt, "failure must prevent all later side effects")
		})
	}
}

func TestExistingEvidenceRetry(t *testing.T) {
	t.Parallel()
	f := newPublication(t)
	for _, file := range []string{f.archive, f.archive + ".sha256"} {
		digest, err := HashFile(file)
		require.NoError(t, err)
		f.release.Assets = append(f.release.Assets, Asset{Name: filepath.Base(file), Size: 1, Digest: "sha256:" + digest})
	}
	require.NoError(t, f.finalize())
	for _, args := range f.calls {
		require.False(t, args[0] == releaseCommand && args[1] == "upload", "verified existing assets are retained")
	}
	require.Equal(t, 1, f.publishCount())
	require.Error(t, uploadEvidence(f.release, filepath.Join(t.TempDir(), "evidence.tar.gz"), nil))
}

func TestInvalidReleaseLookup(t *testing.T) {
	t.Parallel()
	for _, id := range []string{invalidValue, "0", "-1", "9223372036854775808"} {
		_, err := draftRelease(fixtureRepo, fixtureTag, func(...string) ([]byte, error) { return []byte(id), nil })
		require.Error(t, err)
	}
	f := newPublication(t)
	_, err := draftRelease(fixtureRepo, fixtureTag, func(args ...string) ([]byte, error) {
		if args[0] == "api" {
			return []byte("{"), nil
		}
		return f.command(args...)
	})
	require.Error(t, err)
}

func TestEvidenceInputFailures(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"missing root", "no archive", "two archives", "archive directory", "missing checksum", "bad checksum"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newPublication(t)
			switch mode {
			case "missing root":
				f.root = filepath.Join(f.root, "missing")
			case "no archive":
				require.NoError(t, os.Remove(f.archive))
			case "two archives":
				require.NoError(t, os.WriteFile(filepath.Join(f.root, "other.tar.gz"), nil, 0o600))
			case "archive directory":
				require.NoError(t, os.Remove(f.archive))
				require.NoError(t, os.Mkdir(f.archive, 0o700))
			case "missing checksum":
				require.NoError(t, os.Remove(f.archive+".sha256"))
			case "bad checksum":
				require.NoError(t, os.WriteFile(f.archive+".sha256", []byte(invalidValue), 0o600))
			}
			_, _, err := evidenceFiles(f.root)
			require.Error(t, err)
		})
	}
	f := newPublication(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "unrelated"), nil, 0o600))
	archive, checksum, err := evidenceFiles(f.root)
	require.NoError(t, err)
	require.Equal(t, f.archive, archive)
	require.Equal(t, f.archive+".sha256", checksum)
	_, err = HashFile(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}
