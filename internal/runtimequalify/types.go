// Package runtimequalify qualifies native launcher behavior and retains evidence.
package runtimequalify

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
)

const (
	distributedCLI = "distributed-cli"
	policyControl  = "policy-control"
	coldWarmPhases = 2
	Schema         = 1
	Workers        = 16
	Rounds         = 3
	Pass           = "pass"
	Fail           = "fail"
	Incomplete     = "incomplete"
	Source         = "source"
	Candidate      = "candidate"
	Memfd          = "memfd"
	Cache          = "cache"
	Auto           = "auto"
	Required       = "required"
	Sudo           = "sudo"
	Full           = "full"
	Minimal        = "minimal"
	Zstd           = "zstd"
	Startup        = "payload-started\n"
	payloadExit    = 42
	privateMode    = 0o700
	dataMode       = 0o600
	commandTimeout = 2 * time.Minute
)

var sourcePattern = regexp.MustCompile("^[0-9a-f]{40}$")
var digestPattern = regexp.MustCompile("^[0-9a-f]{64}$")
var tagPattern = regexp.MustCompile("^v[0-9]+\\.[0-9]+\\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$")

type Options struct{ Input, Tests, Backend, Output, Dist, Tag, Source, Go string }

func (o Options) Validate(goos, arch string) error {
	if goos != "linux" || !slices.Contains([]string{amd64, arm64}, arch) {
		return errors.New("runtime qualification requires native Linux amd64 or arm64")
	}
	if !slices.Contains([]string{Source, Candidate}, o.Input) || !slices.Contains([]string{"auto", Required}, o.Tests) ||
		!slices.Contains([]string{"userns", Sudo}, o.Backend) || o.Output == "" || o.Go == "" {
		return errors.New("invalid runtime qualification options")
	}
	if o.Input == Candidate && (!filepath.IsAbs(o.Dist) || !tagPattern.MatchString(o.Tag) || !sourcePattern.MatchString(o.Source)) {
		return errors.New("candidate requires a distribution directory, exact tag and full source SHA")
	}
	if o.Input == Source && (o.Dist != "" || o.Tag != "" || o.Source != "") {
		return errors.New("candidate inputs cannot be used in source mode")
	}
	return nil
}

type Configuration struct {
	Format     int    `json:"format"`
	Profile    string `json:"profile"`
	Codec      string `json:"codec"`
	Dictionary bool   `json:"dictionary"`
}

func (c Configuration) Name() string {
	return fmt.Sprintf("v%d-%s-%s-dict%t", c.Format, c.Profile, c.Codec, c.Dictionary)
}
func Configurations() []Configuration {
	var result []Configuration
	for _, format := range []int{1, 2} {
		for _, profile := range []string{Full, Minimal} {
			for _, codec := range []string{"none", "lz4", Zstd} {
				c := Configuration{Format: format, Profile: profile, Codec: codec}
				result = append(result, c)
				if codec == Zstd {
					c.Dictionary = true
					result = append(result, c)
				}
			}
		}
	}
	return result
}

type Case struct {
	Expectation   string        `json:"expected_outcome"`
	ID            string        `json:"id"`
	Configuration Configuration `json:"configuration"`
	Scenario      string        `json:"scenario"`
	Mode          string        `json:"mode"`
	Policy        string        `json:"policy,omitempty"`
	Workers       int           `json:"workers"`
	Runs          int           `json:"runs"`
}

func Cases() []Case {
	var result []Case
	for _, policy := range []string{"", createEPERM, createENOSYS, seal} {
		result = append(result, Case{ID: "policy-control/" + policy, Scenario: policyControl, Policy: policy, Workers: 1, Runs: 1, Mode: Memfd})
	}
	for _, c := range Configurations() {
		add := func(scenario, mode, policy string, workers, runs int) {
			result = append(result, Case{ID: c.Name() + "/" + scenario + "/" + mode, Configuration: c,
				Scenario: scenario, Mode: mode, Policy: policy, Workers: workers, Runs: runs})
		}
		for round := range Rounds {
			for _, mode := range []string{Cache, Auto} {
				policy := ""
				if mode == Auto {
					policy = createEPERM
				}
				add(fmt.Sprintf("concurrent-%d", round), mode, policy, Workers, coldWarmPhases)
			}
		}
		for _, mode := range []string{Memfd, Cache, Auto} {
			for _, policy := range []string{"", createEPERM, createENOSYS, seal, execFirst, execAll} {
				name := policy
				if name == "" {
					name = "unrestricted"
				}
				add(name, mode, policy, 1, 1)
			}
			for _, scenario := range []string{procNoexec, procMissing, procInaccessible, corruptPayload} {
				add(scenario, mode, "", 1, 1)
			}
			if c.Dictionary {
				add(corruptDictionary, mode, "", 1, 1)
			}
		}
		for _, mode := range []string{Memfd, Auto} {
			add(fdPressure, mode, fdPressure, 1, 1)
		}
		for _, mode := range []string{Cache, Auto} {
			policy := ""
			if mode == Auto {
				policy = createEPERM
			}
			for _, scenario := range []string{Full, readOnly, readOnlyWarm, noExec, corruptCache, symlink, fifo, insecure} {
				add(scenario, mode, policy, 1, 1)
			}
		}
		for _, mode := range []string{Memfd, Auto} {
			add(noExec, mode, "", 1, 1)
		}
		result[len(result)-1].ID += "-unrestricted"
		add(busy, Cache, "", 1, coldWarmPhases)
		for _, mode := range []string{Memfd, Cache} {
			add(observe, mode, observe, 1, 1)
			add("exit-42", mode, "", 1, 1)
			add("signal", mode, "", 1, 1)
			add(capability, mode, "", 1, 1)
		}
	}
	for _, mode := range []string{Memfd, Cache, Auto} {
		result = append(result, Case{ID: "distributed-cli/" + mode, Scenario: distributedCLI,
			Mode: mode, Policy: observe, Workers: 1, Runs: 1})
	}
	for i, c := range result {
		switch {
		case c.Scenario == policyControl:
			result[i].Expectation = "independently measure kernel memfd and seal errno"
		case c.Scenario == distributedCLI:
			result[i].Expectation = "unmodified CLI detects native architecture"
		case c.Scenario == busy:
			result[i].Expectation = "ETXTBSY without startup, then one successful startup after writer closes"
		case expectedFailure(c, 0) != nil:
			result[i].Expectation = "launcher refuses before application startup: " + strings.Join(expectedFailure(c, 0), ", ")
		default:
			result[i].Expectation = "one verified application startup per worker and phase; preserve application result"
		}
	}
	return result
}

type Asset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type Release struct {
	ID     int64   `json:"id"`
	Tag    string  `json:"tag_name"`
	Draft  bool    `json:"draft"`
	Assets []Asset `json:"assets"`
}
type Lineage struct {
	Archive       string   `json:"parent_archive,omitempty"`
	ArchiveSHA256 string   `json:"parent_archive_sha256,omitempty"`
	Bundle        string   `json:"bundle"`
	BundleSHA256  string   `json:"bundle_sha256"`
	StubSHA256    string   `json:"stub_sha256"`
	PayloadSHA256 string   `json:"payload_sha256"`
	PayloadSize   int64    `json:"payload_size"`
	SelectedTier  string   `json:"selected_tier"`
	Arguments     []string `json:"arguments"`
}
type Evidence struct {
	ExecutedSHA256 string               `json:"executed_sha256"`
	Case           Case                 `json:"case"`
	Status         string               `json:"status"`
	Reason         string               `json:"reason,omitempty"`
	Lineage        Lineage              `json:"lineage"`
	Command        []string             `json:"command"`
	Request        mountfixture.Request `json:"request"`
	Result         mountfixture.Result  `json:"result"`
	Stderr         string               `json:"helper_stderr,omitempty"`
	Duration       time.Duration        `json:"duration_ns"`
}
type Summary struct {
	Schema          int               `json:"schema"`
	Status          string            `json:"status"`
	Input           string            `json:"input"`
	Architecture    string            `json:"architecture"`
	Backend         string            `json:"backend"`
	Source          string            `json:"source"`
	Dirty           bool              `json:"dirty"`
	Tag             string            `json:"tag,omitempty"`
	Kernel          string            `json:"kernel"`
	PageSize        int               `json:"page_size"`
	Toolchain       string            `json:"toolchain"`
	BuildSettings   map[string]string `json:"build_settings"`
	Policy          string            `json:"memfd_noexec"`
	RunID           string            `json:"run_id,omitempty"`
	Attempt         string            `json:"run_attempt,omitempty"`
	Release         Release           `json:"release,omitempty"`
	ChecksumsSHA256 string            `json:"checksums_sha256,omitempty"`
	Assets          map[string]string `json:"authenticated_assets,omitempty"`
	Products        map[string]string `json:"products_sha256"`
	Expected        []Case            `json:"expected"`
	Results         []Evidence        `json:"results"`
	Error           string            `json:"error,omitempty"`
}

func ValidateSummary(s Summary) error {
	if s.Schema != Schema || s.Status != Pass || !slices.Contains([]string{Source, Candidate}, s.Input) ||
		!slices.Contains([]string{amd64, arm64}, s.Architecture) || !sourcePattern.MatchString(s.Source) ||
		!nativeKernel(s.Kernel, s.Architecture) || s.PageSize <= 0 || s.Toolchain != "go version go1.27.1 linux/"+s.Architecture {
		return errors.New("invalid runtime qualification identity or completion")
	}
	for _, name := range []string{"microfat", fullStub, minimalStub, "reporter", "mount-runner"} {
		if !digestPattern.MatchString(s.Products[name]) {
			return errors.New("missing product identity")
		}
	}
	wanted := Cases()
	if len(s.Expected) != len(wanted) || len(s.Results) != len(wanted) {
		return errors.New("incomplete runtime matrix")
	}
	expected := make(map[string]Case, len(wanted))
	for i, c := range wanted {
		if s.Expected[i] != c {
			return errors.New("runtime manifest differs from required matrix")
		}
		expected[c.ID] = c
	}
	for _, e := range s.Results {
		c, ok := expected[e.Case.ID]
		if !ok || c != e.Case || e.Status != Pass || e.Reason != "" {
			return errors.New("unsuccessful or duplicate runtime case")
		}
		if err := ValidateEvidence(e); err != nil {
			return fmt.Errorf("%s: %w", c.ID, err)
		}
		if err := validateLineage(s, e); err != nil {
			return err
		}
		delete(expected, c.ID)
	}
	if len(expected) != 0 {
		return errors.New("missing runtime cases")
	}
	if s.Input == Candidate {
		return ValidateCandidate(s)
	}
	return nil
}

func nativeKernel(kernel, arch string) bool {
	want := map[string]string{amd64: "x86_64", arm64: "aarch64"}[arch]
	return want != "" && slices.Contains(strings.Fields(kernel), want)
}

func validateLineage(s Summary, e Evidence) error {
	if e.Case.Scenario == policyControl {
		if e.Lineage.BundleSHA256 != s.Products["reporter"] {
			return errors.New("independent policy control substituted")
		}
		return nil
	}
	if e.Case.Scenario == distributedCLI {
		if e.Lineage.BundleSHA256 != s.Products["microfat"] {
			return errors.New("distributed CLI was substituted")
		}
		if s.Input == Candidate && (!digestPattern.MatchString(e.Lineage.PayloadSHA256) || e.Lineage.PayloadSize <= 0) {
			return errors.New("candidate CLI lacks independently inspected payload")
		}
		for _, run := range e.Result.Executions {
			var detected struct{ Arch string }
			if err := json.Unmarshal([]byte(run.Stdout), &detected); err != nil || detected.Arch != s.Architecture {
				return errors.New("CLI did not report the native architecture")
			}
		}
		return nil
	}
	stub := fullStub
	if e.Case.Configuration.Profile == Minimal {
		stub += "-minimal"
	}
	if e.Lineage.StubSHA256 != s.Products[stub] || e.Lineage.PayloadSHA256 != s.Products["reporter"] {
		return errors.New("derived fixture has different parents")
	}
	if s.Input == Candidate {
		contract, err := releasecheck.NewReleaseContract(s.Tag)
		if err != nil {
			return err
		}
		name := contract.ExpectedArchives[s.Architecture]
		if e.Lineage.Archive != name || e.Lineage.ArchiveSHA256 != s.Assets[name] {
			return errors.New("derived fixture does not descend from authenticated archive")
		}
	}
	return nil
}

func ValidateCandidate(s Summary) error {
	if s.Dirty || !tagPattern.MatchString(s.Tag) || s.Release.ID <= 0 || s.Release.Tag != s.Tag || !s.Release.Draft ||
		!digestPattern.MatchString(s.ChecksumsSHA256) || len(s.Assets) == 0 {
		return errors.New("invalid authenticated candidate provenance")
	}
	contract, err := releasecheck.NewReleaseContract(s.Tag)
	if err != nil {
		return err
	}
	if len(s.Assets) != len(contract.ExpectedPayloadNames) {
		return errors.New("incomplete signed product inventory")
	}
	for name := range contract.ExpectedPayloadNames {
		if !digestPattern.MatchString(s.Assets[name]) {
			return errors.New("missing authenticated product")
		}
	}
	return validateCandidateAssets(s)
}

func validateCandidateAssets(s Summary) error {
	seen := map[string]bool{}
	for _, a := range s.Release.Assets {
		if a.ID <= 0 || a.Size <= 0 || seen[a.Name] {
			return errors.New("invalid or duplicate candidate asset")
		}
		seen[a.Name] = true
		if hash, ok := s.Assets[a.Name]; ok && a.Digest != "sha256:"+hash {
			return errors.New("candidate asset digest mismatch")
		}
		if a.Name == "checksums.txt" && a.Digest != "sha256:"+s.ChecksumsSHA256 {
			return errors.New("candidate checksum asset changed")
		}
		if a.Name == "checksums.txt.sig" && !digestPattern.MatchString(strings.TrimPrefix(a.Digest, "sha256:")) {
			return errors.New("missing signature asset digest")
		}
	}
	for name := range s.Assets {
		if !seen[name] {
			return errors.New("authenticated candidate asset missing")
		}
	}
	if !seen["checksums.txt"] || !seen["checksums.txt.sig"] {
		return errors.New("candidate authentication assets missing")
	}
	return nil
}
