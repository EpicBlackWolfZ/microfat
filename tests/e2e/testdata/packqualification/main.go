//go:build linux

// Command packqualification runs the frozen native parallel-Pack study.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

const (
	blocks           = 24
	familyCount      = 4
	replicas         = 2
	architectures    = 2
	variantsPerGroup = 3
	mebibyte         = 1024 * 1024
	privateDir       = 0o700
	privateFile      = 0o600
	serialArm        = "serial"
	defaultArm       = "default"
)

var (
	families       = []string{"actionlint", "fortio", "govulncheck", "task"}
	binaryProfiles = []string{"stripped", "unstripped"}
	packProfiles   = []string{"latency", "balanced", "size"}
	cpuBudgets     = []int{1, 4}
	memoryBudgets  = []int{128, 256, 512, 0}
)

type cell struct {
	BinaryProfile string `json:"binary_profile"`
	PackProfile   string `json:"pack_profile"`
	CPUs          int    `json:"cpu_budget"`
	MemoryMiB     int    `json:"memory_mib"`
}

type pair struct {
	Cell  cell      `json:"cell"`
	Block int       `json:"block"`
	Arms  [2]string `json:"order"`
}

type manifest struct {
	Schema   int    `json:"schema_version"`
	Go       string `json:"go_version"`
	Arch     string `json:"architecture"`
	Revision string `json:"source_revision"`
	Variants []struct {
		Family  string
		Profile string
		Level   string
		Path    string
		SHA256  string
		Bytes   int64
	} `json:"variants"`
}

type workerInput struct {
	Arch, Family, Profile, Stub, Output, Cgroup string
	Variants                                    map[string]string
	Serial                                      bool
}

type workerResult struct {
	PackNS   int64    `json:"pack_ns"`
	Workers  int      `json:"workers"`
	Cgroup   string   `json:"cgroup"`
	Error    string   `json:"error,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type campaign struct {
	Corpus, Output, Root, Arch, Revision, Family, Replica string
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "benchmark" {
		if err := runChild(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	var c campaign
	flag.StringVar(&c.Corpus, "corpus", "", "prepared native corpus")
	flag.StringVar(&c.Output, "output", "", "new evidence directory")
	flag.StringVar(&c.Root, "cgroup-root", "", "owned, delegated cgroup v2 root")
	flag.StringVar(&c.Arch, "arch", "", "native architecture")
	flag.StringVar(&c.Revision, "revision", "", "exact producing Git commit")
	flag.StringVar(&c.Family, "family", "", "one pinned corpus family")
	flag.StringVar(&c.Replica, "replica", "", "independent VM replica, 0 or 1")
	worker := flag.String("worker", "", "internal worker input JSON")
	flag.Parse()
	var err error
	if *worker != "" {
		err = runWorker(*worker)
	} else {
		err = c.run()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runWorker(path string) error {
	var input workerInput
	if err := readJSON(path, &input); err != nil {
		return err
	}
	if err := verifyWorkerCgroup(input); err != nil {
		return err
	}
	out := workerResult{Cgroup: input.Cgroup}
	options := pack.DefaultOptions()
	epoch := int64(0)
	options.CreatedUnix = &epoch
	options.TargetArch, options.AppName, options.Profile = input.Arch, input.Family, input.Profile
	options.StubPath, options.OutputPath, options.Variants = input.Stub, input.Output, input.Variants
	if input.Serial {
		options.VariantWorkerLimit = 1
	}
	options.VariantWorkers = func(count int) { out.Workers = count }
	options.WarnFunc = func(format string, args ...any) { out.Warnings = append(out.Warnings, fmt.Sprintf(format, args...)) }
	start := time.Now()
	_, err := pack.Pack(options)
	out.PackNS = time.Since(start).Nanoseconds()
	if err != nil {
		out.Error = err.Error()
	}
	return errors.Join(err, json.MarshalWrite(os.Stdout, out))
}

func (c campaign) validate() error {
	if !filepath.IsAbs(c.Root) || !strings.HasPrefix(c.Root, "/sys/fs/cgroup/microfat-ci-") ||
		filepath.Clean(c.Root) != c.Root || filepath.Dir(c.Root) != "/sys/fs/cgroup" {
		return errors.New("an owned, delegated benchmark cgroup root is required")
	}
	if c.Arch != runtime.GOARCH || (c.Arch != "amd64" && c.Arch != "arm64") ||
		!slices.Contains(families, c.Family) || (c.Replica != "0" && c.Replica != "1") ||
		c.Revision == "" || !filepath.IsAbs(c.Corpus) || !filepath.IsAbs(c.Output) {
		return errors.New("native architecture, revision, pinned family, VM replica and absolute paths are required")
	}
	cpus, err := system.SupportedAffinity(nil)
	if err != nil || len(cpus) < cpuBudgets[len(cpuBudgets)-1] {
		return errors.Join(errors.New("four usable native CPUs are required"), err)
	}
	return nil
}

func plan() []pair {
	var cells []cell
	for _, binaryProfile := range binaryProfiles {
		for _, packProfile := range packProfiles {
			for _, cpu := range cpuBudgets {
				for _, memory := range memoryBudgets {
					cells = append(cells, cell{binaryProfile, packProfile, cpu, memory})
				}
			}
		}
	}
	trials := make([]pair, 0, len(cells)*blocks)
	for block := range blocks {
		for offset := range cells {
			vari := cells[(offset+block)%len(cells)]
			if block%2 == 0 {
				trials = append(trials, pair{vari, block, [2]string{serialArm, defaultArm}})
			} else {
				trials = append(trials, pair{vari, block, [2]string{defaultArm, serialArm}})
			}
		}
	}
	return trials
}

func (c campaign) loadCorpus() (map[string]map[string]string, error) {
	var m manifest
	if err := readJSON(filepath.Join(c.Corpus, "manifest.json"), &m); err != nil {
		return nil, err
	}
	if m.Schema != 2 || m.Go != runtime.Version() || m.Arch != c.Arch || m.Revision != c.Revision {
		return nil, errors.New("corpus does not match this toolchain, architecture or source revision")
	}
	levels := []string{"v1", "v2", "v3"}
	if c.Arch == "arm64" {
		levels = []string{"v8.0", "v8.1", "v8.2"}
	}
	groups := make(map[string]map[string]string)
	for _, profile := range binaryProfiles {
		groups[profile] = make(map[string]string)
	}
	for _, v := range m.Variants {
		group := groups[v.Profile]
		if v.Family != c.Family || group == nil {
			continue
		}
		expected := filepath.Join(c.Corpus, c.Family+"-"+v.Profile+"-"+v.Level)
		if !slices.Contains(levels, v.Level) || v.Path != expected || group[v.Level] != "" {
			return nil, errors.New("unexpected or duplicate corpus variant")
		}
		hash, size, err := identify(v.Path)
		if err != nil || hash != v.SHA256 || size != v.Bytes {
			return nil, errors.Join(fmt.Errorf("corpus variant changed: %s", v.Path), err)
		}
		group[v.Level] = v.Path
	}
	for _, group := range groups {
		if len(group) != variantsPerGroup {
			return nil, errors.New("missing pinned corpus variants")
		}
	}
	return groups, nil
}

func (c campaign) run() error {
	if err := c.validate(); err != nil {
		return err
	}
	if err := os.Mkdir(c.Output, privateDir); err != nil {
		return err
	}
	trials := plan()
	if err := saveJSON(filepath.Join(c.Output, "plan.json"), trials); err != nil {
		return err
	}
	if err := saveJSON(filepath.Join(c.Output, "campaign.json"), c); err != nil {
		return err
	}
	groups, err := c.loadCorpus()
	if err != nil {
		return err
	}
	results, err := c.collect(context.Background(), trials, groups)
	summary := summarize(results, trials)
	summaryErr := saveJSON(filepath.Join(c.Output, "summary.json"), summary)
	if summary.Decision != decisionPass {
		err = errors.Join(err, fmt.Errorf("shard decision: %s", summary.Decision))
	}
	return errors.Join(err, summaryErr)
}

func readJSON(path string, dst any) error {
	data, err := os.ReadFile(path) // #nosec G304 -- explicit inputs and files from the owned research directory.
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func saveJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFile) // #nosec G304 -- private evidence path.
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	return errors.Join(writeErr, file.Sync(), file.Close())
}

func identify(path string) (string, int64, error) {
	file, err := os.Open(path) // #nosec G304 -- selected corpus input or privately generated output.
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, file)
	return hex.EncodeToString(hash.Sum(nil)), size, errors.Join(copyErr, file.Close())
}
