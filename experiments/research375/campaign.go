package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
)

type host struct {
	GoVersion    string            `json:"go_version"`
	GOARCH       string            `json:"goarch"`
	CPUs         int               `json:"logical_cpus"`
	GOMAXPROCS   int               `json:"gomaxprocs"`
	Limits       cgroup.Limits     `json:"cgroup_limits"`
	LimitsError  string            `json:"cgroup_limits_error,omitempty"`
	Observations map[string]string `json:"observations"`
}

func observeHost() host {
	paths := []string{"/proc/self/cgroup", "/proc/self/status", "/proc/pressure/cpu", "/proc/pressure/memory"}
	if data, err := readBounded("/proc/self/cgroup", workerConfigLimit); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if path, found := strings.CutPrefix(line, "0::/"); found {
				for _, name := range []string{"cpu.stat", "memory.current", "memory.peak", "memory.events", "cpu.pressure", "memory.pressure"} {
					paths = append(paths, filepath.Join("/sys/fs/cgroup", path, name))
				}
			}
		}
	}
	return observeHostSources(paths,
		cgroup.ReadLimits)
}

func observeHostSources(paths []string, limits func() (cgroup.Limits, error)) host {
	h := host{GoVersion: runtime.Version(), GOARCH: runtime.GOARCH, CPUs: runtime.NumCPU(),
		GOMAXPROCS: runtime.GOMAXPROCS(0), Observations: map[string]string{}}
	var err error
	h.Limits, err = limits()
	if err != nil {
		h.LimitsError = err.Error()
	}
	for _, path := range paths {
		data, err := readBounded(path, workerConfigLimit)
		if err != nil {
			h.Observations[path] = "unavailable: " + err.Error()
		} else {
			h.Observations[path] = string(data)
		}
	}
	return h
}

type batch struct {
	BlockID          string        `json:"block_id"`
	Mode             string        `json:"mode"`
	RequestedThreads int           `json:"requested_threads"`
	OuterWorkers     int           `json:"outer_workers"`
	BurstSize        int           `json:"burst_size"`
	DurationNS       int64         `json:"duration_ns"`
	Before           host          `json:"before"`
	After            host          `json:"after"`
	Samples          []observation `json:"samples"`
}

type report struct {
	SchemaVersion       int       `json:"schema_version"`
	Issue               int       `json:"issue"`
	SourceRevision      string    `json:"source_revision"`
	HarnessSHA256       string    `json:"harness_sha256"`
	CodecVersion        string    `json:"codec_version"`
	Family              string    `json:"family"`
	Inputs              []payload `json:"inputs"`
	DictionarySHA256    string    `json:"dictionary_sha256,omitempty"`
	DictionaryBytes     int       `json:"dictionary_bytes"`
	CPUAllocationBudget int       `json:"encoder_cpu_allocation_budget"`
	ArrivalGapNS        int64     `json:"arrival_gap_ns"`
	Controls            string    `json:"controls"`
	Omitted             []string  `json:"omitted_allocations"`
	Batches             []batch   `json:"batches"`
	Error               string    `json:"error,omitempty"`
}

func campaign(ctx context.Context, c config) (result report, resultErr error) {
	c.Inputs = append(stringsFlag(nil), c.Inputs...)
	dir, err := os.MkdirTemp("", "microfat-research375-")
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(dir)) }()
	executable, err := os.Executable()
	if err != nil {
		return result, err
	}
	self, err := os.ReadFile(executable) // #nosec G304 -- resolved current executable for immutable provenance.
	if err != nil {
		return result, err
	}
	result = report{SchemaVersion: 1, Issue: researchIssue, SourceRevision: c.Source, HarnessSHA256: digest(self),
		CodecVersion: "github.com/klauspost/compress v1.20.3", Family: c.Family,
		CPUAllocationBudget: c.Budget, ArrivalGapNS: c.Gap.Nanoseconds(),
		Controls: "observed inherited controls; no quota, affinity, pressure or memory ceiling is imposed by this harness"}
	total := 0
	for i, path := range c.Inputs {
		p, err := loadPayload(path)
		if err != nil {
			return result, err
		}
		total += p.Bytes
		if total > maxTotalBytes {
			return result, errors.New("corpus exceeds 64MiB total bound")
		}
		// Snapshot once: later source changes cannot silently change cohorts.
		p.Path = filepath.Join(dir, fmt.Sprintf("input-%d", i))
		if err := os.WriteFile(p.Path, p.Data, privateMode); err != nil {
			return result, err
		}
		result.Inputs = append(result.Inputs, payload{Path: path, SHA256: p.SHA256, Bytes: p.Bytes})
		c.Inputs[i] = p.Path
	}
	if c.Dictionary != "" {
		dict, err := readBounded(c.Dictionary, maxDictBytes)
		if err != nil {
			return result, err
		}
		result.DictionarySHA256, result.DictionaryBytes = digest(dict), len(dict)
		c.Dictionary = filepath.Join(dir, "dictionary")
		if err := os.WriteFile(c.Dictionary, dict, privateMode); err != nil {
			return result, err
		}
	}
	result.Batches, result.Omitted = encoderBatches(ctx, c, executable, dir, result.Inputs)
	decodeBatches, err := decoderBatches(ctx, c, executable, dir, result.Inputs)
	result.Batches = append(result.Batches, decodeBatches...)
	return result, err
}

func encoderBatches(ctx context.Context, c config, executable, dir string, inputs []payload) ([]batch, []string) {
	var batches []batch
	var omitted []string
	for trial := range c.Trials {
		if ctx.Err() != nil {
			return batches, omitted
		}
		// Rotate enumeration to reduce fixed search-order bias; this is still exploratory.
		for offset := range len(c.Encoders) {
			threads := c.Encoders[(offset+trial)%len(c.Encoders)]
			for _, outer := range c.Workers {
				if outer*effective(threads, false) > c.Budget {
					if trial == 0 {
						omitted = append(omitted, fmt.Sprintf("workers=%d threads=%d: over CPU budget", outer, threads))
					}
					continue
				}
				id := fmt.Sprintf("encode-%d-%d-%d", trial, threads, outer)
				requests := make([]request, len(inputs))
				for i, p := range inputs {
					requests[i] = request{Mode: modeEncode, Input: c.Inputs[i], Output: filepath.Join(dir, fmt.Sprintf("frame-%d", i)),
						Dictionary: c.Dictionary, SHA256: p.SHA256, Bytes: p.Bytes, Threads: threads, Level: c.Level}
				}
				b := runBatch(ctx, c, executable, dir, id, requests, outer)
				batches = append(batches, b)
			}
		}
	}
	return batches, omitted
}

func decoderBatches(ctx context.Context, c config, executable, dir string, inputs []payload) ([]batch, error) {
	var batches []batch
	// Decode a fixed single-thread baseline frame to isolate decoder settings from encoder output changes.
	for i, p := range inputs {
		if err := ctx.Err(); err != nil {
			return batches, err
		}
		data, err := readBounded(c.Inputs[i], maxInputBytes)
		if err != nil {
			return batches, err
		}
		var dict []byte
		if c.Dictionary != "" {
			dict, err = readBounded(c.Dictionary, maxDictBytes)
			if err != nil {
				return batches, err
			}
		}
		frame, err := encode(data, dict, c.Level, 1)
		if err != nil {
			return batches, err
		}
		path := filepath.Join(dir, fmt.Sprintf("decode-frame-%d", i))
		if err := os.WriteFile(path, frame, privateMode); err != nil {
			return batches, err
		}
		for trial := range c.Trials {
			if err := ctx.Err(); err != nil {
				return batches, err
			}
			for offset := range len(c.Decoders) {
				threads := c.Decoders[(offset+trial)%len(c.Decoders)]
				for _, burst := range c.Bursts {
					id := fmt.Sprintf("decode-%d-%d-%d-%d", i, trial, threads, burst)
					requests := make([]request, burst)
					for j := range requests {
						requests[j] = request{Mode: modeDecode, Input: path, Dictionary: c.Dictionary, SHA256: p.SHA256,
							Bytes: p.Bytes, Threads: threads, Execute: c.Execute, Args: c.PayloadArgs, ReadyOutput: c.ReadyOutput}
					}
					b := runBatch(ctx, c, executable, dir, id, requests, burst)
					for j := range b.Samples {
						b.Samples[j].InputIndex = i
						b.Samples[j].CompressedBytes = len(frame)
						b.Samples[j].CompressedSHA256 = digest(frame)
					}
					batches = append(batches, b)
				}
			}
		}
	}
	return batches, nil
}

func runBatch(ctx context.Context, c config, executable, dir, id string, requests []request, parallel int) batch {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	b := batch{BlockID: id, Mode: requests[0].Mode, RequestedThreads: requests[0].Threads,
		OuterWorkers: parallel, BurstSize: len(requests), Before: observeHost(), Samples: make([]observation, len(requests))}
	start := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, parallel)
	ready := make(chan struct{}, len(requests))
	releases := make([]chan struct{}, len(requests))
	for i := range releases {
		releases[i] = make(chan struct{})
	}
	go func() {
		if requests[0].Mode == modeDecode {
			for range requests {
				<-ready
			}
		}
		for i := range releases {
			if i > 0 && c.Gap > 0 && requests[0].Mode == modeDecode {
				timer := time.NewTimer(c.Gap)
				select {
				case <-timer.C:
				case <-ctx.Done():
				}
				timer.Stop()
			}
			close(releases[i])
		}
	}()
	for i, r := range requests {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			path := filepath.Join(dir, fmt.Sprintf("request-%d.json", i))
			data, err := json.Marshal(r)
			if err == nil {
				err = os.WriteFile(path, data, privateMode) // #nosec G703 -- private generated workspace and fixed indexed filename.
			}
			if err != nil {
				b.Samples[i] = observation{Failed: true, Error: err.Error()}
				ready <- struct{}{}
				return
			}
			o := process(ctx, executable, path, releases[i], ready, start)
			o.BlockID, o.InputIndex, o.BurstSize = id, i, len(requests)
			o.MeasurementKind, o.State = modeEncode, "encoder-process"
			if r.Mode == modeDecode {
				o.MeasurementKind, o.State = "decode+sha256", "buffered-stream-decode"
			}
			if r.Execute {
				o.MeasurementKind, o.State = "decode+sha256+materialize+native-child-ready", "temporary-file-pipeline"
			}
			b.Samples[i] = o
		})
	}
	wg.Wait()
	b.DurationNS, b.After = time.Since(start).Nanoseconds(), observeHost()
	return b
}
