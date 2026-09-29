package runtimequalify

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/require"
)

const (
	testCandidateTag = "v0.3.0"
	testWrongValue   = "wrong"
	testStatus       = "status"
	testDiff         = "diff"
	testExit         = "exit"
)

func completeCandidate(t *testing.T) Summary {
	t.Helper()
	s := validSummary(t)
	s.Input, s.Tag, s.ChecksumsSHA256 = Candidate, testCandidateTag, testHash
	s.Assets = map[string]string{}
	s.Release = Release{ID: 1, Tag: s.Tag, Draft: true}
	contract, err := releasecheck.NewReleaseContract(s.Tag)
	require.NoError(t, err)
	for name := range contract.ExpectedPayloadNames {
		s.Assets[name] = testHash
		s.Release.Assets = append(s.Release.Assets,
			Asset{ID: int64(len(s.Release.Assets) + 1), Name: name, Size: 1, Digest: "sha256:" + testHash})
	}
	for _, name := range []string{"checksums.txt", "checksums.txt.sig"} {
		s.Release.Assets = append(s.Release.Assets,
			Asset{ID: int64(len(s.Release.Assets) + 1), Name: name, Size: 1, Digest: "sha256:" + testHash})
	}
	for i := range s.Results {
		e := &s.Results[i]
		if e.Case.Scenario == distributedCLI {
			*e = candidateCLIEvidence(t, e.Case.Mode)
		}
		e.Lineage.Archive = contract.ExpectedArchives[s.Architecture]
		e.Lineage.ArchiveSHA256 = testHash
	}
	return s
}

func TestCompleteCandidateRejectsSubstitution(t *testing.T) {
	t.Parallel()
	base := completeCandidate(t)
	require.NoError(t, ValidateSummary(base))
	bad := base
	bad.Tag = ""
	require.Error(t, validateLineage(bad, base.Results[4]))
	bad = base
	bad.Assets = maps.Clone(base.Assets)
	delete(bad.Assets, base.Results[4].Lineage.Archive)
	require.Error(t, ValidateCandidate(bad))
	for _, name := range []string{"product", "policy-probe", "cli", "cli-payload", "cli-architecture", "stub",
		"archive", "archive-hash", "invalid-tag", "invalid-inventory", "invalid-digest", "asset-digest", "signature-digest", "missing-asset"} {
		t.Run(name, func(t *testing.T) {
			s := base
			s.Results = slices.Clone(base.Results)
			s.Products, s.Assets = maps.Clone(base.Products), maps.Clone(base.Assets)
			s.Release.Assets = slices.Clone(base.Release.Assets)
			last := len(s.Results) - 1
			switch name {
			case "product":
				s.Products["reporter"] = ""
			case "policy-probe":
				s.Results[0].Lineage.BundleSHA256 = strings.Repeat("c", 64)
				require.Error(t, validateLineage(s, s.Results[0]))
				return
			case "cli":
				s.Products["microfat"] = strings.Repeat("c", 64)
				require.Error(t, validateLineage(s, s.Results[last]))
				return
			case "cli-payload":
				s.Results[last].Lineage.PayloadSHA256 = ""
				require.Error(t, validateLineage(s, s.Results[last]))
				return
			case "cli-architecture":
				s.Architecture = arm64
				require.Error(t, validateLineage(s, s.Results[last]))
				return
			case "stub":
				s.Products[fullStub] = strings.Repeat("c", 64)
				require.Error(t, validateLineage(s, s.Results[4]))
				return
			case "archive":
				s.Results[4].Lineage.Archive = "substitute"
			case "archive-hash":
				s.Results[4].Lineage.ArchiveSHA256 = ""
			case "invalid-tag":
				s.Tag = "v999999999999999999999999.3.0"
				s.Release.Tag = s.Tag
				require.Error(t, ValidateCandidate(s))
				require.Error(t, validateLineage(s, s.Results[4]))
				return
			case "invalid-inventory":
				delete(s.Assets, s.Results[4].Lineage.Archive)
			case "invalid-digest":
				s.Assets[s.Results[4].Lineage.Archive] = testWrongValue
			case "asset-digest":
				s.Release.Assets[0].Digest = testWrongValue
			case "signature-digest":
				s.Release.Assets[len(s.Release.Assets)-1].Digest = ""
			case "missing-asset":
				s.Release.Assets = s.Release.Assets[1:]
			}
			require.Error(t, ValidateSummary(s))
		})
	}
}

func TestCompletionAlwaysPersistsItsReason(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"success", "incomplete", "invalid-summary", "failed-case"} {
		t.Run(mode, func(t *testing.T) {
			c := &controller{options: Options{Output: t.TempDir()}, summary: validSummary(t)}
			var failure error
			switch mode {
			case "incomplete":
				c.summary.Status = Incomplete
			case "invalid-summary":
				c.summary.Results = nil
			case "failed-case":
				failure = errors.New("worker failed")
			}
			err := c.complete(failure)
			if mode == "invalid-summary" || mode == "failed-case" {
				require.Error(t, err)
				require.Equal(t, Fail, c.summary.Status)
				require.Equal(t, err.Error(), c.summary.Error)
			} else {
				require.NoError(t, err)
			}
			data, err := os.ReadFile(filepath.Join(c.options.Output, "summary.json"))
			require.NoError(t, err)
			require.Contains(t, string(data), c.summary.Status)
		})
	}
}
