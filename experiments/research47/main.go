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

func summarizeGate(groups []groupResult) gateResult {
	type profileGate struct{ costed, zero, modeled bool }
	families := make(map[string]map[string]profileGate)
	for _, g := range groups {
		profiles := families[g.Family]
		if profiles == nil {
			profiles = make(map[string]profileGate)
			families[g.Family] = profiles
		}
		p := profileGate{}
		if g.Error == "" {
			for _, c := range g.Candidates {
				if c.Error == "" && c.Method != methodBase {
					p.costed = p.costed || c.SizePass
					p.zero = p.zero || c.OptimisticSizePass
					p.modeled = p.modeled || c.ScreeningPass
				}
			}
		}
		profiles[g.Profile] = p
	}
	r := gateResult{}
	for family, profiles := range families {
		a, b := profiles[stripped], profiles[unstripped]
		f := familyGate{family, a.costed && b.costed, a.zero && b.zero, a.modeled && b.modeled}
		r.Families = append(r.Families, f)
		if f.CostedSize {
			r.CostedSizePass++
		}
		if f.ZeroDecoder {
			r.ZeroDecoderPass++
		}
		if f.ModeledBudgets {
			r.ModeledBudgetsPass++
		}
	}
	sort.Slice(r.Families, func(i, j int) bool { return r.Families[i].Family < r.Families[j].Family })
	complete := len(families) == maxFamilies
	r.CostedOpportunityPass = complete && r.CostedSizePass >= requiredFamilies
	r.ModeledScreenPass = complete && r.ModeledBudgetsPass >= requiredFamilies
	switch {
	case !complete:
		r.Decision = "incomplete corpus; no portfolio gate decision"
	case r.ZeroDecoderPass < requiredFamilies:
		r.Decision = "stop #379 on this candidate space: even zero-decoder size scenario misses gate"
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
	Groups         []groupResult  `json:"groups"`
	Gate           gateResult     `json:"gate"`
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
		o.Manifest == "" || o.Stub == "" || o.Output == "" || runtime.Version() != goVersion {
		return screenReport{}, errors.New("explicit paths, exact producer revision and Go 1.27.3 required")
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
	r := screenReport{SchemaVersion: 1, Producer: o.Source, GoVersion: runtime.Version(), CodecVersion: codecModuleVersion(),
		GOMAXPROCS: runtime.GOMAXPROCS(0), ManifestSHA256: manifestSHA, Corpus: m, Stub: o.Stub, StubBytes: int64(len(stub)),
		StubSHA256: digest(stub), Protocol: frozenProtocol()}
	for _, g := range groups {
		dir := filepath.Join(output, g.family+"-"+g.profile)
		if err = os.Mkdir(dir, privateDirMode); err != nil {
			return r, err
		}
		r.Groups = append(r.Groups, screenGroup(g, m.Architecture, o.Stub, dir, stub))
	}
	r.Gate = summarizeGate(r.Groups)
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
		data, encodeErr := json.Marshal(r.Gate)
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
