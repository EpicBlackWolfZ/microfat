// Command research375pool compares complete packing with an opt-in in-process pool.
package main

import (
	"context"
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
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"golang.org/x/sys/unix"
)

const (
	mib              = int64(1024 * 1024)
	maxJSON          = 4 * mib
	maxInput         = 32 * mib
	maxCorpus        = 64 * mib
	maxTrials        = 10
	defaultTrials    = 5
	exitUsage        = 2
	privateDirMode   = 0o700
	privateFileMode  = 0o600
	maxArtifact      = 128 * mib
	defaultWorkspace = 64 * mib
	nsPerMicrosecond = 1000
	nsPerSecond      = 1000000000
)

// Set by the reproducible build target; ordinary unit-test binaries leave it empty.
var producerRevision string

type request struct {
	Stub        string                   `json:"stub"`
	Output      string                   `json:"output"`
	AppName     string                   `json:"app_name"`
	Arch        string                   `json:"arch"`
	Variants    map[string]string        `json:"variants"`
	Compression string                   `json:"compression"`
	Level       string                   `json:"level"`
	Dictionary  bool                     `json:"dictionary"`
	Format      int                      `json:"format"`
	Pool        *pack.EncoderPoolOptions `json:"pool,omitempty"`
}

func (r request) options() pack.Options {
	o := pack.DefaultOptions()
	o.StubPath, o.OutputPath, o.AppName, o.TargetArch = r.Stub, r.Output, r.AppName, r.Arch
	o.Variants, o.Compression, o.CompressionLevel, o.EnableDict = r.Variants, r.Compression, r.Level, r.Dictionary
	o.FormatVersion = r.Format
	return o
}

type workerResult struct {
	PackNS          int64                  `json:"pack_ns"`
	PackCPUNS       int64                  `json:"pack_cpu_ns"`
	MaxRSSKiB       int64                  `json:"max_rss_kib"`
	GoVersion       string                 `json:"go_version"`
	GOMAXPROCS      int                    `json:"gomaxprocs"`
	GoMemLimitBytes int64                  `json:"go_mem_limit_bytes"`
	Index           *format.Index          `json:"index,omitempty"`
	Pool            *pack.EncoderPoolStats `json:"pool_stats,omitempty"`
	Error           string                 `json:"error,omitempty"`
}

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("research375pool", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "campaign", "campaign, worker, corpus, matrix, limits, summary, calibrate, freeze, confirm, or confirm-summary")
	requestPath := fs.String("request", "", "bounded worker request JSON")
	manifest := fs.String("manifest", "", "hash-pinned corpus manifest")
	stub := fs.String("stub", "", "full launcher stub")
	minimal := fs.String("minimal-stub", "", "minimal launcher stub for matrix")
	output := fs.String("output", "", "new output directory; existing paths are refused")
	source := fs.String("source-revision", producerRevision, "exact producer revision, checked against the research build")
	corpus := fs.String("corpus", "", "prebuilt corpus directory for manifest generation")
	trials := fs.Int("trials", defaultTrials, "fixed blocks per controlled cohort, at most 10")
	execute := fs.Bool("execute", false, "execute trusted native corpus in untimed matrix checks only")
	policyPath := fs.String("policy", "", "frozen training-only policy JSON for confirmation")
	profile := fs.String("profile", profileStripped, "stripped or unstripped corpus profile")
	blocks := fs.Int("blocks", maxConfirmationBlocks, "fixed confirmation blocks (30 planned); calibration requires3")
	hostID := fs.String("host-id", "", "predeclared native host/VM replica identifier")
	family := fs.String("family", "", "one known family for the bounded correctness matrix")
	architecture := fs.String("architecture", runtime.GOARCH, "native corpus architecture: amd64 or arm64")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "unexpected positional arguments")
		return exitUsage
	}
	var value any
	var err error
	switch *mode {
	case "worker":
		value, err = runWorker(ctx, *requestPath)
	case "corpus":
		value, err = buildManifestForArch(*corpus, *source, *architecture)
	case "summary":
		var report campaignReport
		if err = readJSON(*requestPath, &report); err == nil {
			value, err = summarizeReport(report)
		}
	case "freeze":
		var report campaignReport
		if err = readJSON(*requestPath, &report); err == nil {
			var hash string
			hash, _, err = digestFile(*requestPath, maxJSON)
			if err == nil {
				value, err = freezeEncoderPolicy(report, hash)
			}
			if err == nil {
				err = writeJSON(*output, value)
			}
		}
	case "confirm-summary":
		var report campaignReport
		if err = readJSON(*requestPath, &report); err == nil {
			value, err = summarizeConfirmation(report)
		}
	case studyCalibration, studyConfirmation:
		var policy *frozenEncoderPolicy
		if *mode == studyConfirmation {
			policy = &frozenEncoderPolicy{}
			err = readJSON(*policyPath, policy)
		}
		if err == nil {
			value, err = runCampaign(ctx, campaignOptions{Manifest: *manifest, Stub: *stub, Output: *output, Source: *source,
				Trials: *blocks, Study: *mode, Profile: *profile, Policy: policy, HostID: *hostID}, RunResourceTrial)
		}
	case "campaign", "matrix", "limits":
		value, err = runCampaign(ctx, campaignOptions{Manifest: *manifest, Stub: *stub, Minimal: *minimal,
			Output: *output, Source: *source, Trials: *trials, Matrix: *mode == "matrix", Limits: *mode == "limits",
			Execute: *execute, Profile: *profile, Family: *family},
			RunResourceTrial)
	default:
		err = errors.New("unsupported mode")
	}
	if value != nil {
		data, encodeErr := json.Marshal(value)
		if encodeErr != nil {
			err = errors.Join(err, encodeErr)
		} else {
			_, writeErr := fmt.Fprintln(stdout, string(data))
			err = errors.Join(err, writeErr)
		}
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func readJSON(path string, dst any) error {
	f, err := inputfile.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxJSON+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > maxJSON {
		return errors.New("JSON exceeds bound")
	}
	return json.Unmarshal(b, dst, json.RejectUnknownMembers(true))
}

func writeJSON(path string, value any) error {
	b, err := json.Marshal(value, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return errors.Join(err, f.Close())
}

func cpuNS(r unix.Rusage) int64 {
	return (r.Utime.Sec+r.Stime.Sec)*nsPerSecond + (r.Utime.Usec+r.Stime.Usec)*nsPerMicrosecond
}

func runWorker(ctx context.Context, path string) (workerResult, error) {
	out := workerResult{GoVersion: runtime.Version(), GOMAXPROCS: runtime.GOMAXPROCS(0), GoMemLimitBytes: debug.SetMemoryLimit(-1)}
	var req request
	if err := readJSON(path, &req); err != nil {
		return out, err
	}
	var before, after unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &before); err != nil {
		return out, err
	}
	started := time.Now()
	var err error
	if req.Pool == nil {
		out.Index, err = pack.Pack(req.options())
	} else {
		out.Index, out.Pool, err = pack.PackWithEncoderPool(ctx, req.options(), *req.Pool)
	}
	out.PackNS = time.Since(started).Nanoseconds()
	usageErr := unix.Getrusage(unix.RUSAGE_SELF, &after)
	out.PackCPUNS, out.MaxRSSKiB = cpuNS(after)-cpuNS(before), after.Maxrss
	if err != nil {
		out.Error = err.Error()
	}
	return out, errors.Join(err, usageErr)
}
