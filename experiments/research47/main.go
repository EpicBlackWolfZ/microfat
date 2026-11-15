// Command research47 screens exact inter-variant sharing before format design.
package main

import (
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
)

const (
	sha256Bytes     = sha256.Size
	privateDirMode  = 0o700
	privateFileMode = 0o600
	exitUsage       = 2
	reportSchema    = 2
)

// Supplied by the research build target. Test binaries leave this empty.
var producerRevision string

type screenOptions struct{ Manifest, Stub, Output, Source string }

type protocol struct {
	MinimumSaving         float64 `json:"minimum_complete_saving_fraction"`
	RequiredFamilies      int     `json:"required_families_both_profiles"`
	DecoderReserveBytes   int     `json:"estimated_decoder_reserve_bytes"`
	LargeReserveBytes     int     `json:"sensitivity_decoder_reserve_bytes"`
	CodecReserveBytes     int     `json:"modeled_codec_reserve_bytes"`
	WorkspaceBudgetBytes  int     `json:"modeled_workspace_budget_bytes"`
	MaximumModeledRise    float64 `json:"maximum_modeled_cold_path_increase"`
	ReadMiBPerSecond      float64 `json:"assumed_read_mib_per_second"`
	DecodeMiBPerSecond    float64 `json:"assumed_decode_mib_per_second"`
	HashMiBPerSecond      float64 `json:"assumed_hash_mib_per_second"`
	CopyWriteMiBPerSecond float64 `json:"assumed_copy_write_mib_per_second"`
	RealRuntimeGate       string  `json:"real_startup_and_rss_gate"`
}

func frozenProtocol() protocol {
	return protocol{minimumSaving, requiredFamilies, decoderReserve, largeReserve, codecReserve, workspaceBudget,
		maximumModeledRise, readMiBPerSecond, decodeMiBPerSecond, hashMiBPerSecond, writeMiBPerSecond,
		"unresolved: no runtime decoder, startup measurement or measured RSS bound"}
}

type familyGate struct {
	Family         string `json:"family"`
	CostedSize     bool   `json:"costed_size_both_profiles"`
	ZeroDecoder    bool   `json:"zero_decoder_size_both_profiles"`
	ModeledBudgets bool   `json:"size_and_modeled_budgets_both_profiles"`
}

type gateResult struct {
	Families              []familyGate `json:"families"`
	CostedSizePass        int          `json:"costed_size_families"`
	ZeroDecoderPass       int          `json:"zero_decoder_size_families"`
	ModeledBudgetsPass    int          `json:"size_and_modeled_budgets_families"`
	CostedOpportunityPass bool         `json:"costed_size_opportunity_pass"`
	ModeledScreenPass     bool         `json:"costed_size_and_modeled_budgets_pass"`
	Decision              string       `json:"decision"`
}

func completeCohort(groups []groupResult) bool {
	profiles := make(map[[2]string]bool)
	for _, g := range groups {
		if g.Error != "" || len(g.Candidates) == 0 {
			return false
		}
		for _, c := range g.Candidates {
			if c.Error != "" {
				return false
			}
		}
		if g.Family != "boundary" {
			key := [2]string{g.Family, g.Profile}
			if profiles[key] || (g.Profile != stripped && g.Profile != unstripped) {
				return false
			}
			profiles[key] = true
		}
	}
	const profilesPerFamily = 2
	return len(profiles) == maxFamilies*profilesPerFamily
}

func summarizeGate(groups []groupResult) gateResult {
	type profileGate struct{ costed, zero, modeled bool }
	type representation struct{ method, base string }
	type familyCount struct{ costed, zero, modeled int }
	families := make(map[string]map[string]map[representation]profileGate)
	for _, g := range groups {
		if g.Family == "boundary" {
			continue
		}
		profiles := families[g.Family]
		if profiles == nil {
			profiles = make(map[string]map[representation]profileGate)
			families[g.Family] = profiles
		}
		p := make(map[representation]profileGate)
		if g.Error == "" {
			for _, c := range g.Candidates {
				if c.Error == "" {
					p[representation{c.Method, c.Base}] = profileGate{c.SizePass, c.OptimisticSizePass, c.ScreeningPass}
				}
			}
		}
		profiles[g.Profile] = p
	}
	r := gateResult{}
	byEncoding := make(map[representation]familyCount)
	for family, profiles := range families {
		f := familyGate{Family: family}
		for encoding, a := range profiles[stripped] {
			b := profiles[unstripped][encoding]
			count := byEncoding[encoding]
			if a.costed && b.costed {
				f.CostedSize = true
				count.costed++
			}
			if a.zero && b.zero {
				f.ZeroDecoder = true
				count.zero++
			}
			if a.modeled && b.modeled {
				f.ModeledBudgets = true
				count.modeled++
			}
			byEncoding[encoding] = count
		}
		r.Families = append(r.Families, f)
	}
	for _, count := range byEncoding {
		r.CostedSizePass = max(r.CostedSizePass, count.costed)
		r.ZeroDecoderPass = max(r.ZeroDecoderPass, count.zero)
		r.ModeledBudgetsPass = max(r.ModeledBudgetsPass, count.modeled)
	}
	sort.Slice(r.Families, func(i, j int) bool { return r.Families[i].Family < r.Families[j].Family })
	complete := len(families) == maxFamilies && completeCohort(groups)
	r.CostedOpportunityPass = complete && r.CostedSizePass >= requiredFamilies
	r.ModeledScreenPass = complete && r.ModeledBudgetsPass >= requiredFamilies
	switch {
	case !complete:
		r.Decision = "incomplete corpus or failed observation; no portfolio gate decision"
	case r.ZeroDecoderPass < requiredFamilies:
		r.Decision = "stop on this candidate space: even zero-decoder size scenario misses gate"
	case !r.CostedOpportunityPass:
		r.Decision = "stop before decoder: estimated decoder cost erases useful size opportunity"
	case !r.ModeledScreenPass:
		r.Decision = "size opportunity exists; modeled selected-target budgets fail; no decoder authorization"
	default:
		r.Decision = "offline opportunity clears; compare simpler #45 frontier; real startup/RSS gate unresolved"
	}
	return r
}

type screenReport struct {
	SchemaVersion  int            `json:"schema_version"`
	Producer       string         `json:"producer_revision"`
	GoVersion      string         `json:"go_version"`
	CodecVersion   string         `json:"codec_module_version"`
	GOMAXPROCS     int            `json:"gomaxprocs"`
	ManifestSHA256 string         `json:"manifest_sha256"`
	Corpus         corpusManifest `json:"corpus"`
	Stub           string         `json:"stub_path"`
	StubBytes      int64          `json:"stub_bytes"`
	StubSHA256     string         `json:"stub_sha256"`
	Protocol       protocol       `json:"protocol"`
	Levels         []levelResult  `json:"levels"`
}

type levelResult struct {
	Level  string        `json:"level"`
	Groups []groupResult `json:"groups"`
	Gate   gateResult    `json:"gate"`
}

func codecModuleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if ok {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/klauspost/compress" {
				return dep.Version
			}
		}
	}
	return "unavailable in test binary"
}

func runScreen(o screenOptions) (screenReport, error) {
	const revisionBytes = 20
	if !validHex(o.Source, revisionBytes) || (producerRevision != "" && producerRevision != o.Source) ||
		o.Manifest == "" || o.Stub == "" || o.Output == "" {
		return screenReport{}, errors.New("explicit paths and exact producer revision required")
	}
	m, groups, manifestSHA, err := loadManifest(o.Manifest)
	if err != nil {
		return screenReport{}, err
	}
	stub, err := readBounded(o.Stub, maxInput)
	if err != nil {
		return screenReport{}, err
	}
	if err = validateELF(stub, m.Architecture); err != nil {
		return screenReport{}, err
	}
	output, err := filepath.Abs(o.Output)
	if err != nil {
		return screenReport{}, err
	}
	if err = os.Mkdir(output, privateDirMode); err != nil {
		return screenReport{}, err
	}
	r := screenReport{SchemaVersion: reportSchema, Producer: o.Source, GoVersion: runtime.Version(), CodecVersion: codecModuleVersion(),
		GOMAXPROCS: runtime.GOMAXPROCS(0), ManifestSHA256: manifestSHA, Corpus: m, Stub: o.Stub, StubBytes: int64(len(stub)),
		StubSHA256: digest(stub), Protocol: frozenProtocol()}
	for _, level := range []string{levelBetter, levelBest} {
		c := levelResult{Level: level}
		for _, g := range groups {
			dir := filepath.Join(output, level, g.family+"-"+g.profile)
			if err = os.MkdirAll(dir, privateDirMode); err != nil {
				return r, err
			}
			c.Groups = append(c.Groups, screenGroup(g, m.Architecture, o.Stub, dir, stub, level))
		}
		c.Gate = summarizeGate(c.Groups)
		r.Levels = append(r.Levels, c)
	}
	data, err := json.Marshal(r, jsontext.WithIndent("  "))
	if err == nil {
		err = writeNew(filepath.Join(output, "report.json"), data)
	}
	return r, err
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("research47", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o screenOptions
	fs.StringVar(&o.Manifest, "manifest", "", "checksum-pinned schema-v2 corpus manifest")
	fs.StringVar(&o.Stub, "stub", "", "baseline launcher ELF")
	fs.StringVar(&o.Output, "output", "", "new output directory")
	fs.StringVar(&o.Source, "source-revision", producerRevision, "exact committed producer revision")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "unexpected positional arguments")
		return exitUsage
	}
	r, err := runScreen(o)
	if err == nil {
		decisions := make(map[string]gateResult)
		for _, level := range r.Levels {
			decisions[level.Level] = level.Gate
		}
		data, encodeErr := json.Marshal(decisions)
		if encodeErr == nil {
			_, encodeErr = fmt.Fprintln(stdout, string(data))
		}
		err = encodeErr
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
