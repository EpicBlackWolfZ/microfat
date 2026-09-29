package releaseworkflow

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/EpicBlackWolfZ/microfat/internal/runtimequalify"
	"github.com/stretchr/testify/require"
)

func runtimeRelease(t *testing.T) Release {
	t.Helper()
	c, err := releasecheck.NewReleaseContract("0.3.0")
	require.NoError(t, err)
	r := Release{ID: 19, Draft: true, TagName: "v0.3.0"}
	names := []string{"checksums.txt", "checksums.txt.sig"}
	for name := range c.ExpectedPayloadNames {
		names = append(names, name)
	}
	for _, name := range names {
		r.Assets = append(r.Assets, Asset{ID: int64(len(r.Assets) + 1), Name: name, Size: 20, Digest: "sha256:" + strings.Repeat("a", 64)})
	}
	return r
}

func runtimeIdentity(r Release, run WorkflowRun) runtimequalify.Summary {
	s := runtimequalify.Summary{Input: runtimequalify.Candidate, Source: run.HeadSHA, Tag: r.TagName, Architecture: "amd64",
		RunID: strconv.FormatInt(run.ID, 10), Attempt: strconv.FormatInt(run.Attempt, 10), Release: runtimequalify.Release{ID: r.ID},
		Assets: map[string]string{}}
	for _, a := range r.Assets {
		s.Release.Assets = append(s.Release.Assets, runtimequalify.Asset{ID: a.ID, Name: a.Name, Size: a.Size, Digest: a.Digest})
		if a.Name != "checksums.txt" && a.Name != "checksums.txt.sig" {
			s.Assets[a.Name] = strings.TrimPrefix(a.Digest, "sha256:")
		}
	}
	return s
}

func TestRuntimeGateRejectsMissingOrSkippedArchitecture(t *testing.T) {
	t.Parallel()
	jobs := []Job{{Name: runtimeJob("amd64"), Conclusion: success}, {Name: runtimeJob("arm64"), Conclusion: success}}
	require.NoError(t, ValidateRuntimeJobs(jobs))
	for _, name := range []string{"amd64", "arm64"} {
		t.Run(name, func(t *testing.T) {
			for _, conclusion := range []string{"skipped", "failure", "cancelled", ""} {
				c := slices.Clone(jobs)
				for i := range c {
					if c[i].Name == runtimeJob(name) {
						c[i].Conclusion = conclusion
					}
				}
				require.Error(t, ValidateRuntimeJobs(c))
			}
		})
	}
	require.Error(t, ValidateRuntimeJobs(nil))
	require.Error(t, ValidateRuntimeJobs(jobs[:1]))
	require.Error(t, ValidateRuntimeJobs(jobs[1:]))
	require.Error(t, ValidateRuntimeJobs(append(slices.Clone(jobs), jobs[0])))
}

func TestRuntimeGateBindsExactCandidateAndAttempt(t *testing.T) {
	t.Parallel()
	release := runtimeRelease(t)
	run := successfulBuild()
	run.ID, run.Attempt = 27, 2
	base := runtimeIdentity(release, run)
	require.NoError(t, validateRuntimeIdentity(base, release, run, "amd64"))
	for _, scenario := range []string{"source-mode", "wrong-source", "wrong-tag", "architecture", "producer", "attempt", "release", "asset-id",
		"asset-size", "asset-digest", "missing-digest", "missing-asset", "signature"} {
		t.Run(scenario, func(t *testing.T) {
			s := base
			r := release
			r.Assets = slices.Clone(release.Assets)
			switch scenario {
			case "source-mode":
				s.Input = runtimequalify.Source
			case "wrong-source":
				s.Source = strings.Repeat("b", 40)
			case "wrong-tag":
				s.Tag = "v0.3.1"
			case "architecture":
				s.Architecture = "arm64"
			case "producer":
				s.RunID = "28"
			case "attempt":
				s.Attempt = "1"
			case "release":
				s.Release.ID++
			case "asset-id":
				r.Assets[0].ID++
			case "asset-size":
				r.Assets[0].Size++
			case "asset-digest":
				r.Assets[0].Digest = "sha256:" + strings.Repeat("b", 64)
			case "missing-digest":
				r.Assets[0].Digest = ""
			case "missing-asset":
				r.Assets = r.Assets[1:]
			case "signature":
				r.Assets[1].ID++
			}
			require.Error(t, validateRuntimeIdentity(s, r, run, "amd64"))
		})
	}
	require.Error(t, ValidateRuntimeSummary(base, release, run, "amd64"), "identity alone is not completed runtime evidence")
}

func TestRuntimeInventoryRecheckedBeforePublication(t *testing.T) {
	t.Parallel()
	before := runtimeRelease(t)
	require.NoError(t, sameRuntimeInventory(before, before))
	after := before
	after.Assets = append(slices.Clone(before.Assets), Asset{Name: "supplemental-evidence", ID: 90, Size: 1})
	require.NoError(t, sameRuntimeInventory(before, after))
	for _, scenario := range []string{"id", "wrong-tag", "published", "immutable", "missing", "replaced", "empty-digest"} {
		t.Run(scenario, func(t *testing.T) {
			after := before
			after.Assets = slices.Clone(before.Assets)
			switch scenario {
			case "id":
				after.ID++
			case "wrong-tag":
				after.TagName = "v0.3.1"
			case "published":
				after.Draft = false
			case "immutable":
				after.Immutable = true
			case "missing":
				after.Assets = after.Assets[1:]
			case "replaced":
				after.Assets[0].ID++
			case "empty-digest":
				after.Assets[0].Digest = ""
			}
			require.Error(t, sameRuntimeInventory(before, after))
		})
	}
}

func TestRuntimeEvidenceParsingAndArchives(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"missing", "invalid", "unknown-field", "trailing", "duplicate", "symlink", "valid"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "summary.json")
			switch scenario {
			case "missing":
			case "invalid":
				require.NoError(t, os.WriteFile(path, []byte("{"), runtimeEvidenceMode))
			case "unknown-field":
				require.NoError(t, os.WriteFile(path, []byte(`{"unexpected":true}`), runtimeEvidenceMode))
			case "trailing":
				require.NoError(t, os.WriteFile(path, []byte("{}{}"), runtimeEvidenceMode))
			case "duplicate":
				require.NoError(t, os.WriteFile(path, []byte("{}"), runtimeEvidenceMode))
				require.NoError(t, os.Mkdir(filepath.Join(root, "nested"), 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(root, "nested", "summary.json"), []byte("{}"), runtimeEvidenceMode))
			case "symlink":
				require.NoError(t, os.Symlink("/missing", path))
			case "valid":
				require.NoError(t, runtimequalify.WriteJSON(path, runtimequalify.Summary{Schema: 1}))
			}
			s, err := readRuntimeSummary(root)
			if scenario == "valid" {
				require.NoError(t, err)
				require.Equal(t, 1, s.Schema)
			} else {
				require.Error(t, err)
			}
		})
	}
	root := t.TempDir()
	path := filepath.Join(root, "record.json")
	require.NoError(t, os.WriteFile(path, []byte("evidence"), runtimeEvidenceMode))
	archive := filepath.Join(t.TempDir(), "runtime.tar.gz")
	require.NoError(t, archiveRuntimeEvidence(root, archive))
	file, err := os.Open(archive)
	require.NoError(t, err)
	defer file.Close()
	z, err := gzip.NewReader(file)
	require.NoError(t, err)
	defer z.Close()
	reader := tar.NewReader(z)
	header, err := reader.Next()
	require.NoError(t, err)
	require.Equal(t, "record.json", header.Name)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "evidence", string(data))
	_, err = reader.Next()
	require.ErrorIs(t, err, io.EOF)
	require.Error(t, archiveRuntimeEvidence(root, archive))
	require.NoError(t, os.Symlink("/missing", filepath.Join(root, "link")))
	require.Error(t, archiveRuntimeEvidence(root, filepath.Join(t.TempDir(), "bad.tar.gz")))
}

func TestRequiredProducerCannotUseHistoricalBypass(t *testing.T) {
	t.Parallel()
	files, err := qualifyRuntimeRelease(fixtureRepo, "v0.2.9", fixtureSHA, nil, Release{}, nil)
	require.NoError(t, err)
	require.Empty(t, files)
	release := runtimeRelease(t)
	_, err = qualifyRuntimeRelease(fixtureRepo, release.TagName, fixtureSHA, nil, release, nil)
	require.Error(t, err)
	run := successfulBuild()
	run.HeadBranch, run.ID, run.Attempt = release.TagName, 27, 2
	for _, scenario := range []string{"api-error", "missing-jobs", "truncated-jobs", "download", "bad-summary"} {
		t.Run(scenario, func(t *testing.T) {
			gh := func(args ...string) ([]byte, error) {
				if args[0] == "api" {
					require.Contains(t, args[1], "/runs/27/attempts/2/jobs")
					if scenario == "api-error" {
						return nil, errors.New("API failure")
					}
					jobs := []Job{{Name: runtimeJob("amd64"), Conclusion: success}, {Name: runtimeJob("arm64"), Conclusion: success}}
					total := len(jobs)
					if scenario == "missing-jobs" {
						jobs = nil
					}
					if scenario == "truncated-jobs" {
						total++
					}
					return json.Marshal(map[string]any{"jobs": jobs, "total_count": total})
				}
				require.Equal(t, []string{"run", "download"}, args[:2])
				require.Contains(t, args, "runtime-candidate-amd64-27-2")
				if scenario == "download" {
					return nil, errors.New("missing artifact")
				}
				target := args[len(args)-1]
				require.NoError(t, os.MkdirAll(target, 0o700))
				require.NoError(t, runtimequalify.WriteJSON(filepath.Join(target, "summary.json"), runtimequalify.Summary{}))
				return nil, nil
			}
			_, err := qualifyRuntimeRelease(fixtureRepo, release.TagName, fixtureSHA, []WorkflowRun{run}, release, gh)
			require.Error(t, err)
		})
	}
}

func TestBothFinalizersRequireNativeCandidateJobs(t *testing.T) {
	t.Parallel()
	for _, recovery := range []bool{false, true} {
		t.Run(strconv.FormatBool(recovery), func(t *testing.T) {
			release := runtimeRelease(t)
			run := successfulBuild()
			run.ID, run.Attempt, run.HeadBranch = 27, 2, release.TagName
			env := newPublication(t).env
			env["RELEASE_TAG"], env["GITHUB_REF"] = release.TagName, "refs/tags/"+release.TagName
			options := FinalizeOptions{Root: t.TempDir()}
			if recovery {
				maps.Copy(env, recoveryEnvironment())
				options.RecoveryRun, options.RecoveryAttempt = "1", "2"
			}
			gateQueried := false
			gh := func(args ...string) ([]byte, error) {
				if args[0] == releaseCommand {
					require.Equal(t, "view", args[1], "no upload or publication before runtime qualification")
					return []byte("19"), nil
				}
				endpoint := args[1]
				switch {
				case strings.Contains(endpoint, "/runs/27/attempts/2/jobs"):
					gateQueried = true
					return jsonBytes(t, map[string]any{"jobs": []Job{{Name: runtimeJob("amd64"), Conclusion: success},
						{Name: runtimeJob("arm64"), Conclusion: "skipped"}}}), nil
				case strings.Contains(endpoint, "/runs/1/attempts/2/jobs"):
					return jsonBytes(t, map[string]any{"jobs": append(completeJobs(), Job{Name: VerificationJob, Conclusion: success})}), nil
				case strings.Contains(endpoint, "/runs/1/attempts/2"):
					measurement := measurementRun()
					measurement.HeadBranch = release.TagName
					return jsonBytes(t, measurement), nil
				case strings.Contains(endpoint, "/commits/"):
					return jsonBytes(t, map[string]string{"sha": fixtureSHA}), nil
				case strings.Contains(endpoint, "/actions/workflows/"):
					return jsonBytes(t, map[string]any{"workflow_runs": []WorkflowRun{run}}), nil
				case strings.Contains(endpoint, "/releases/19"):
					return jsonBytes(t, release), nil
				default:
					t.Fatalf("unexpected command: %v", args)
					return nil, nil
				}
			}
			require.ErrorContains(t, Finalize(options, lookup(env), gh), "native runtime qualification")
			require.True(t, gateQueried)
		})
	}
}
