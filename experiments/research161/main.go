// Command research161 screens bounded dictionary sampling alternatives for issue #161.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

const (
	maxInputBytes = 64 * 1024 * 1024
	maxVariants   = 8
	minVariants   = 2
	defaultSizes  = "32768,114688"
	privateDir    = 0o700
	privateFile   = 0o600
	issueNumber   = 161
)

type input struct {
	Family string `json:"family"`
	Level  string `json:"level"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
	data   []byte
}

type manifest struct {
	SchemaVersion  int     `json:"schema_version"`
	SourceRevision string  `json:"source_revision"`
	GoVersion      string  `json:"go_version"`
	Variants       []input `json:"variants"`
}

type options struct {
	manifestPath string
	family       string
	stubPath     string
	output       string
	sizes        string
	level        string
	arch         string
}

type report struct {
	SchemaVersion  int      `json:"schema_version"`
	Issue          int      `json:"issue"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	SourceRevision string   `json:"source_revision"`
	CorpusGo       string   `json:"corpus_go_version"`
	RunnerGo       string   `json:"runner_go_version"`
	CodecVersion   string   `json:"codec_version"`
	Family         string   `json:"family"`
	TargetArch     string   `json:"target_arch"`
	Level          string   `json:"compression_level"`
	GOMAXPROCS     int      `json:"gomaxprocs"`
	StubSHA256     string   `json:"stub_sha256"`
	StubBytes      int      `json:"stub_bytes"`
	Inputs         []input  `json:"inputs"`
	Results        []result `json:"results"`
	Smallest       string   `json:"smallest_verified_artifact"`
	Limitations    []string `json:"limitations"`
}

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("research161", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.manifestPath, "manifest", "", "pinned corpus manifest JSON")
	flags.StringVar(&opts.family, "family", "", "one corpus program family")
	flags.StringVar(&opts.stubPath, "stub", "", "matching microfat stub ELF")
	flags.StringVar(&opts.output, "output", "", "new output directory (never overwritten)")
	flags.StringVar(&opts.sizes, "sizes", defaultSizes, "comma-separated history byte budgets")
	flags.StringVar(&opts.level, "level", "better", "fixed Zstd compression level")
	flags.StringVar(&opts.arch, "arch", runtime.GOARCH, "target ELF architecture")
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	if flags.NArg() != 0 || opts.manifestPath == "" || opts.family == "" || opts.stubPath == "" || opts.output == "" {
		return opts, errors.New("--manifest, --family, --stub and --output are required; positional arguments are unsupported")
	}
	if _, err := codec.NewZstdCodec().MapLevel(opts.level); err != nil {
		return opts, err
	}
	return opts, nil
}

func parseSizes(value string) ([]int, error) {
	var sizes []int
	for _, part := range strings.Split(value, ",") {
		size, err := strconv.Atoi(part)
		if err != nil || size < sampleBytes || size > codec.MaxDictSize {
			return nil, fmt.Errorf("invalid history budget %q: require %d..%d bytes", part, sampleBytes, codec.MaxDictSize)
		}
		if slices.Contains(sizes, size) {
			return nil, fmt.Errorf("duplicate history budget %d", size)
		}
		sizes = append(sizes, size)
	}
	return sizes, nil
}

func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- Explicit user-selected corpus input; size and file type are bounded below.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxInputBytes {
		return nil, errors.New("input must be a regular file at most 64 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInputBytes {
		return nil, errors.New("input exceeded 64 MiB while reading")
	}
	return data, nil
}

func loadCorpus(opts options) (manifest, []input, string, error) {
	data, err := readBounded(opts.manifestPath)
	if err != nil {
		return manifest{}, nil, "", err
	}
	var corpus manifest
	if err := json.Unmarshal(data, &corpus); err != nil {
		return corpus, nil, "", err
	}
	if corpus.SchemaVersion != 1 || corpus.SourceRevision == "" || corpus.GoVersion != "go1.27.3" {
		return corpus, nil, "", errors.New("require schema_version=1, source_revision and pinned corpus go_version=go1.27.3")
	}
	var selected []input
	seen := make(map[string]bool)
	for _, item := range corpus.Variants {
		if item.Family != opts.family {
			continue
		}
		if strings.TrimSpace(item.Level) == "" {
			return corpus, nil, "", errors.New("variant level is required")
		}
		item.Level = microarch.Normalize(item.Level)
		if seen[item.Level] || microarch.Rank(opts.arch, item.Level) < 0 {
			return corpus, nil, "", fmt.Errorf("duplicate or invalid variant level %q", item.Level)
		}
		seen[item.Level] = true
		item.data, err = readBounded(item.Path)
		if err != nil {
			return corpus, nil, "", err
		}
		if item.Bytes != len(item.data) || item.SHA256 != digest(item.data) {
			return corpus, nil, "", fmt.Errorf("corpus identity mismatch: %s", item.Path)
		}
		if err := validateELF(item.data, opts.arch); err != nil {
			return corpus, nil, "", fmt.Errorf("variant %s: %w", item.Level, err)
		}
		selected = append(selected, item)
	}
	if len(selected) < minVariants || len(selected) > maxVariants {
		return corpus, nil, "", errors.New("select between two and eight variants from one family")
	}
	slices.SortFunc(selected, func(a, b input) int {
		return microarch.Rank(opts.arch, a.Level) - microarch.Rank(opts.arch, b.Level)
	})
	return corpus, selected, digest(data), nil
}

func run(args []string, stderr io.Writer) error {
	opts, err := parseOptions(args, stderr)
	if err != nil {
		return err
	}
	sizes, err := parseSizes(opts.sizes)
	if err != nil {
		return err
	}
	corpus, inputs, manifestHash, err := loadCorpus(opts)
	if err != nil {
		return err
	}
	stub, err := readBounded(opts.stubPath)
	if err != nil {
		return err
	}
	if err := validateELF(stub, opts.arch); err != nil {
		return fmt.Errorf("stub: %w", err)
	}
	if err := os.Mkdir(opts.output, privateDir); err != nil {
		return err
	}
	rep := report{
		SchemaVersion: 1, Issue: issueNumber, ManifestSHA256: manifestHash, SourceRevision: corpus.SourceRevision,
		CorpusGo: corpus.GoVersion, RunnerGo: runtime.Version(), CodecVersion: codecVersion(), Family: opts.family,
		TargetArch: opts.arch, Level: opts.level, GOMAXPROCS: runtime.GOMAXPROCS(0), StubSHA256: digest(stub),
		StubBytes: len(stub), Inputs: inputs,
		Limitations: []string{
			"A bounded in-memory size screen; all inputs and complete artifacts are retained in memory.",
			"Wall time and allocation counters are one-shot diagnostics, excluding input I/O and publication; they are not process-start p99.",
			"No peak RSS, pack CPU, cgroup stress, launch execution, held-out validation or production-default decision is claimed.",
			"Requested history budgets match; actual history is capped at one quarter of training bytes. Dictionary headers add bytes.",
			"Section sampling changes training contents; other alternatives isolate history selection with uniform contents.",
			"No dictionary is a required candidate. Warm verified-cache launches do not decompress.",
		},
	}
	for _, candidate := range candidates(inputs, sizes) {
		res, artifact := evaluate(candidate, inputs, stub, opts)
		if res.Error == "" {
			if err := os.WriteFile(filepath.Join(opts.output, res.ID+".fat"), artifact, privateDir); err != nil {
				return err
			}
		}
		rep.Results = append(rep.Results, res)
	}
	rep.Smallest = smallest(rep.Results)
	if rep.Smallest == "" {
		return errors.New("all candidates failed; output directory retained for inspection")
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(opts.output, "report.json"), append(data, '\n'), privateFile)
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func codecVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/klauspost/compress" {
				return dep.Version
			}
		}
	}
	return "unavailable"
}

func smallest(results []result) string {
	var best *result
	for i := range results {
		if results[i].Error == "" && (best == nil || results[i].ArtifactBytes < best.ArtifactBytes) {
			best = &results[i]
		}
	}
	if best == nil {
		return ""
	}
	return best.ID
}
