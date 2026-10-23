package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	campaignTestRevision    = "1234567890abcdef1234567890abcdef12345678"
	campaignTestRequestFlag = "--request"
)

func campaignELFFixture(full bool) []byte {
	const (
		headerBytes = 128
		bodyBytes   = 32 * 1024
		baseAddress = 0x400000
	)
	data := make([]byte, headerBytes+bodyBytes)
	copy(data, "\x7fELF")
	data[elf.EI_CLASS], data[elf.EI_DATA], data[elf.EI_VERSION] = byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), 1
	binary.LittleEndian.PutUint16(data[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(data[18:], uint16(elf.EM_X86_64))
	binary.LittleEndian.PutUint32(data[20:], 1)
	binary.LittleEndian.PutUint64(data[24:], baseAddress+120)
	binary.LittleEndian.PutUint64(data[32:], 64)
	binary.LittleEndian.PutUint16(data[52:], 64)
	binary.LittleEndian.PutUint16(data[54:], 56)
	binary.LittleEndian.PutUint16(data[56:], 1)
	binary.LittleEndian.PutUint32(data[64:], uint32(elf.PT_LOAD))
	binary.LittleEndian.PutUint32(data[68:], uint32(elf.PF_R|elf.PF_X))
	binary.LittleEndian.PutUint64(data[80:], baseAddress)
	binary.LittleEndian.PutUint64(data[96:], uint64(len(data)))
	binary.LittleEndian.PutUint64(data[104:], uint64(len(data)))
	binary.LittleEndian.PutUint64(data[112:], 1)
	for i := headerBytes; i < len(data); i++ {
		data[i] = byte((i % 251) ^ (i / 1024))
	}
	if full {
		data = append(data, []byte("a distinct full launcher fixture tail")...)
	}
	return data
}

func storeCampaignJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func campaignFixture(t *testing.T) (campaignOptions, corpusManifest) {
	t.Helper()
	dir := t.TempDir()
	for _, family := range []string{launchTestFamily, familyMicrofat} {
		for _, level := range []string{"v1", "v2", "v3"} {
			data := append(campaignELFFixture(false), []byte(family+"-"+level)...)
			require.NoError(t, os.WriteFile(filepath.Join(dir, family+"-"+level), data, 0o755))
		}
	}
	m, err := buildManifest(dir, campaignTestRevision)
	require.NoError(t, err)
	stub, minimal := filepath.Join(dir, "stub"), filepath.Join(dir, "minimal-stub")
	require.NoError(t, os.WriteFile(stub, campaignELFFixture(true), 0o755))
	require.NoError(t, os.WriteFile(minimal, campaignELFFixture(false), 0o755))
	return campaignOptions{Manifest: filepath.Join(dir, "manifest.json"), Stub: stub, Minimal: minimal,
		Output: filepath.Join(dir, "campaign"), Source: campaignTestRevision, Trials: 1}, m
}

func fixtureWorkerRequest(t *testing.T, opts campaignOptions, corpus corpusManifest, pooled bool) (request, string) {
	t.Helper()
	req := request{Stub: opts.Stub, Output: filepath.Join(t.TempDir(), "artifact"), AppName: launchTestFamily, Arch: "amd64",
		Variants: familyVariants(corpus, launchTestFamily), Compression: codec.AlgorithmZstd, Level: "fastest", Format: format.FormatVersion2}
	if pooled {
		req.Pool = &pack.EncoderPoolOptions{MaxWorkers: 2, InnerConcurrency: 1, CPUBudget: 2,
			MemoryBudgetBytes: 16 * mib, WorkspaceBytesPerEncoder: mib}
	}
	path := filepath.Join(t.TempDir(), "request.json")
	storeCampaignJSON(t, path, req)
	return req, path
}

func TestCorpusManifestIdentityAndStrictJSON(t *testing.T) {
	t.Parallel()
	opts, original := campaignFixture(t)
	m, families, err := loadCorpus(opts.Manifest)
	require.NoError(t, err)
	assert.Equal(t, original, m)
	assert.Equal(t, []string{launchTestFamily, familyMicrofat}, families)
	assert.Len(t, familyVariants(m, launchTestFamily), 3)
	assert.Empty(t, familyVariants(m, "absent"))
	_, err = buildManifest(filepath.Dir(opts.Manifest), campaignTestRevision)
	require.Error(t, err, "published manifest must not be overwritten")
	for _, source := range []string{"", "short", strings.Repeat("g", gitRevisionLength)} {
		assert.False(t, validRevision(source))
		_, err := buildManifest(filepath.Dir(opts.Manifest), source)
		require.Error(t, err)
	}
	_, err = buildManifest("", campaignTestRevision)
	require.Error(t, err)
	_, err = buildManifest(t.TempDir(), campaignTestRevision)
	require.Error(t, err, "all six pinned inputs are required")
	for _, data := range []string{
		`{"schema_version":1,"schema_version":2}`, `{"unknown":true}`, `{} {}`, `invalid`,
	} {
		path := filepath.Join(t.TempDir(), "bad.json")
		require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
		var decoded corpusManifest
		require.Error(t, readJSON(path, &decoded))
	}
	var decoded corpusManifest
	require.Error(t, readJSON(filepath.Join(t.TempDir(), fixtureMissing), &decoded))
	large := filepath.Join(t.TempDir(), "large-json")
	f, err := os.Create(large)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(maxJSON+1))
	require.NoError(t, f.Close())
	require.ErrorContains(t, readJSON(large, &decoded), "JSON exceeds bound")
	fifo := filepath.Join(t.TempDir(), "fifo")
	require.NoError(t, unix.Mkfifo(fifo, 0o600))
	require.Error(t, readJSON(fifo, &decoded), "FIFO JSON must be rejected without waiting")
	_, _, err = digestFile(fifo, maxInput)
	require.Error(t, err)
	_, _, err = digestFile(t.TempDir(), maxInput)
	require.Error(t, err)
	_, _, err = digestFile(opts.Stub, 1)
	require.ErrorContains(t, err, "outside size bound")
	_, _, err = digestFile(filepath.Join(t.TempDir(), fixtureMissing), maxInput)
	require.Error(t, err)
	require.Error(t, writeJSON(filepath.Join(t.TempDir(), "nan"), math.NaN()))
	require.Error(t, writeJSON(filepath.Join(t.TempDir(), "absent", "json"), m))
	require.Error(t, writeJSON(opts.Manifest, m))
}

func TestCorpusManifestRejectsMutationAndMalformedRecords(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*corpusManifest)
	}{
		{"schema", func(m *corpusManifest) { m.SchemaVersion++ }},
		{"toolchain", func(m *corpusManifest) { m.GoVersion = "go1.27.2" }},
		{"revision", func(m *corpusManifest) { m.SourceRevision = "invalid" }},
		{"empty", func(m *corpusManifest) { m.Variants = nil }},
		{"too many", func(m *corpusManifest) { m.Variants = make([]corpusVariant, maxVariants+1) }},
		{"family", func(m *corpusManifest) { m.Variants[0].Family = "../escape" }},
		{"level", func(m *corpusManifest) { m.Variants[0].Level = "v4" }},
		{fixtureDuplicate, func(m *corpusManifest) { m.Variants[1] = m.Variants[0] }},
		{"sha256", func(m *corpusManifest) { m.Variants[0].SHA256 = fixtureWrong }},
		{fixtureBytes, func(m *corpusManifest) { m.Variants[0].Bytes++ }},
		{fixtureMissing, func(m *corpusManifest) { m.Variants[0].Path += fixtureMissing }},
		{fixtureRelative, func(m *corpusManifest) { m.Variants[0].Path = filepath.Base(m.Variants[0].Path) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts, corpus := campaignFixture(t)
			tc.mutate(&corpus)
			storeCampaignJSON(t, opts.Manifest, corpus)
			_, _, err := loadCorpus(opts.Manifest)
			require.Error(t, err)
		})
	}
	opts, corpus := campaignFixture(t)
	require.NoError(t, os.WriteFile(corpus.Variants[0].Path, []byte("mutated after manifest"), 0o755))
	_, _, err := loadCorpus(opts.Manifest)
	require.ErrorContains(t, err, "integrity mismatch")
	// Three maximum-size sparse files exceed the aggregate bound. Hashing streams
	// through the files; no unbounded payload allocation is needed for this check.
	m := corpusManifest{SchemaVersion: 1, SourceRevision: campaignTestRevision, GoVersion: researchGoVersion}
	for _, level := range []string{"v1", "v2", "v3"} {
		path := filepath.Join(t.TempDir(), level)
		file, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, file.Truncate(maxInput))
		require.NoError(t, file.Close())
		hash, size, err := digestFile(path, maxInput)
		require.NoError(t, err)
		m.Variants = append(m.Variants, corpusVariant{Family: launchTestFamily, Level: level, Path: path, SHA256: hash, Bytes: size})
	}
	storeCampaignJSON(t, opts.Manifest, m)
	_, _, err = loadCorpus(opts.Manifest)
	require.ErrorContains(t, err, "total exceeds bound")
}

func TestWorkerActuallyPacksAndVerifiesBothPaths(t *testing.T) {
	t.Parallel()
	for _, pooled := range []bool{false, true} {
		t.Run(fmt.Sprintf("pool=%t", pooled), func(t *testing.T) {
			t.Parallel()
			opts, corpus := campaignFixture(t)
			req, path := fixtureWorkerRequest(t, opts, corpus, pooled)
			result, err := runWorker(context.Background(), path)
			require.NoError(t, err)
			require.Empty(t, result.Error)
			require.NotNil(t, result.Index)
			assert.Equal(t, researchGoVersion, result.GoVersion)
			assert.Positive(t, result.PackNS)
			assert.GreaterOrEqual(t, result.PackCPUNS, int64(0))
			assert.Positive(t, result.MaxRSSKiB)
			assert.Equal(t, pooled, result.Pool != nil)
			artifact, err := verifyArtifact(req, corpus)
			require.NoError(t, err)
			assert.Equal(t, result.Index, artifact.Index)
			assert.Len(t, artifact.CompressedSHA256, 3)
			assert.Len(t, artifact.SHA256, sha256.Size*2)
			assert.Greater(t, artifact.Bytes, int64(0))
			if pooled {
				assert.Equal(t, 3, result.Pool.JobsCompleted)
				assert.Zero(t, result.Pool.ActiveWorkers)
				assert.Zero(t, result.Pool.AdmittedBytes)
				req.Pool.MemoryBudgetBytes = 1
			} else {
				req.Stub += fixtureMissing
			}
			storeCampaignJSON(t, path, req)
			failed, err := runWorker(context.Background(), path)
			require.Error(t, err)
			assert.NotEmpty(t, failed.Error)
			assert.Nil(t, failed.Index)
			assert.Positive(t, failed.PackNS)
		})
	}
	_, err := runWorker(context.Background(), filepath.Join(t.TempDir(), fixtureMissing))
	require.Error(t, err)
}

func TestVerifyArtifactRejectsCorruptionAndCorpusMismatch(t *testing.T) {
	t.Parallel()
	opts, corpus := campaignFixture(t)
	req, requestPath := fixtureWorkerRequest(t, opts, corpus, false)
	req.Compression = codec.AlgorithmNone
	storeCampaignJSON(t, requestPath, req)
	w, err := runWorker(context.Background(), requestPath)
	require.NoError(t, err)
	original, err := os.ReadFile(req.Output)
	require.NoError(t, err)
	missing := req
	missing.Output = filepath.Join(t.TempDir(), fixtureMissing)
	_, err = verifyArtifact(missing, corpus)
	require.Error(t, err)
	for _, data := range [][]byte{[]byte("invalid artifact"), original[:len(original)-1]} {
		path := filepath.Join(t.TempDir(), "corrupt")
		require.NoError(t, os.WriteFile(path, data, 0o600))
		corrupt := req
		corrupt.Output = path
		_, err := verifyArtifact(corrupt, corpus)
		require.Error(t, err)
	}
	for _, mutation := range []string{"count", "sha256", fixtureBytes, "level"} {
		t.Run(mutation, func(t *testing.T) {
			m := corpus
			m.Variants = slices.Clone(corpus.Variants)
			switch mutation {
			case "count":
				m.Variants = m.Variants[1:]
			case "sha256":
				m.Variants[0].SHA256 = strings.Repeat("0", sha256.Size*2)
			case fixtureBytes:
				m.Variants[0].Bytes++
			case "level":
				m.Variants[0].Level = "v0"
			}
			_, err := verifyArtifact(req, m)
			require.Error(t, err)
		})
	}
	original[w.Index.Variants[0].Offset] ^= 1
	require.NoError(t, os.WriteFile(req.Output, original, 0o600))
	_, err = verifyArtifact(req, corpus)
	require.Error(t, err, "decompression/integrity failures must be refused")
}

func TestVerifyArtifactChecksRequestedCodecDictionaryStubAndMetadata(t *testing.T) {
	t.Parallel()
	opts, corpus := campaignFixture(t)
	req, path := fixtureWorkerRequest(t, opts, corpus, false)
	_, err := runWorker(context.Background(), path)
	require.NoError(t, err)
	_, err = verifyArtifact(req, corpus)
	require.NoError(t, err)
	mismatches := []string{"codec", "dictionary", "stub-prefix", "stub-hash", "stub-missing", "stub-long", "format", "app", "arch"}
	for _, mismatch := range mismatches {
		t.Run(mismatch, func(t *testing.T) {
			wrong := req
			switch mismatch {
			case "codec":
				wrong.Compression = codec.AlgorithmNone
			case "dictionary":
				wrong.Dictionary = true
			case "stub-prefix":
				wrong.Stub = opts.Minimal
			case "stub-hash":
				wrong.Stub = filepath.Join(t.TempDir(), "different-stub")
				data := campaignELFFixture(true)
				data[len(data)-1] ^= 1
				require.NoError(t, os.WriteFile(wrong.Stub, data, 0o755))
			case "stub-missing":
				wrong.Stub += fixtureMissing
			case "stub-long":
				wrong.Stub = filepath.Join(t.TempDir(), "long-stub")
				data, err := os.ReadFile(req.Output)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(wrong.Stub, append(data, 0), 0o755))
			case "format":
				wrong.Format = format.FormatVersion1
			case "app":
				wrong.AppName = "wrong-family"
			case "arch":
				wrong.Arch = "arm64"
			}
			_, err := verifyArtifact(wrong, corpus)
			require.Error(t, err)
		})
	}
}

func TestCampaignSpecsBalanceAndRotation(t *testing.T) {
	t.Parallel()
	families := []string{launchTestFamily, familyMicrofat}
	specs := makeSpecs(families, campaignOptions{Trials: 2})
	require.Len(t, specs, 60)
	counts := map[string]int{}
	for _, spec := range specs {
		counts[fmt.Sprintf("%s/%d/%d/%d/%s", spec.Family, spec.Block, spec.CPU, spec.MemoryMiB, spec.Mode)]++
		assert.Equal(t, format.FormatVersion2, spec.Format)
		assert.Equal(t, codec.AlgorithmZstd, spec.Codec)
		assert.False(t, spec.Dictionary)
		if spec.CPU == 1 {
			assert.NotEqual(t, "serial1", spec.Mode)
		}
	}
	for _, count := range counts {
		assert.Equal(t, 1, count)
	}
	assert.NotEqual(t, specs[0].Family+specs[0].Mode, specs[len(specs)/2].Family+specs[len(specs)/2].Mode)
	matrix := makeSpecs(families, campaignOptions{Matrix: true, Trials: 2})
	require.Len(t, matrix, 64)
	for _, spec := range matrix {
		assert.Contains(t, []int{format.FormatVersion1, format.FormatVersion2}, spec.Format)
		assert.Contains(t, []string{summaryFullStub, "minimal"}, spec.StubProfile)
		assert.Contains(t, []string{"current", "pool"}, spec.Mode)
		assert.Contains(t, []string{codec.AlgorithmNone, codec.AlgorithmLZ4, codec.AlgorithmZstd}, spec.Codec)
		assert.Equal(t, 4, spec.CPU)
	}
	assert.Equal(t, codec.AlgorithmZstd, mapCodec("zstd-dict"))
	assert.Equal(t, codec.AlgorithmLZ4, mapCodec(codec.AlgorithmLZ4))
	negative := makeSpecs(families, campaignOptions{Limits: true, Trials: 2})
	require.Len(t, negative, 5)
	for _, spec := range negative {
		assert.Equal(t, familyMicrofat, spec.Family)
		assert.True(t, spec.PriorOutput)
		assert.Equal(t, 4, spec.CPU)
	}
	assert.Equal(t, int64(1), negative[len(negative)-1].AdmissionMiB)
	assert.Equal(t, int64(512), negative[len(negative)-1].MemoryMiB)
}

func TestNegativeControlsRetainOOMAdmissionAndPriorOutput(t *testing.T) {
	t.Parallel()
	opts, _ := campaignFixture(t)
	opts.Limits = true
	runner := func(ctx context.Context, cfg trialResourceConfig, exe string, args, env []string) (resourceTrialResult, error) {
		var req request
		require.NoError(t, readJSON(args[2], &req))
		// Simulate an uncatchable OOM's abandoned input/spool directory. The
		// controller must remove its owned TMPDIR even when no JSON is emitted.
		require.NoError(t, os.WriteFile(filepath.Join(cfg.TrialDir, "tmp", "abandoned-spool"), []byte("partial"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(cfg.TrialDir, ".microfat-spools-owned"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(cfg.TrialDir, ".microfat-spools-owned", "payload"), []byte("partial"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(cfg.TrialDir, ".microfat-pack-owned.tmp"), []byte("partial"), 0o600))
		if cfg.MemoryMaxBytes < 512*mib {
			return resourceTrialResult{OOMKilled: true, ExitCode: 137}, nil
		}
		require.NotNil(t, req.Pool)
		assert.Equal(t, mib, req.Pool.MemoryBudgetBytes)
		return fixtureTrialRunner(t)(ctx, cfg, exe, args, env)
	}
	r, err := runCampaign(context.Background(), opts, runner)
	require.NoError(t, err)
	require.Len(t, r.Observations, 5)
	for i, obs := range r.Observations {
		assert.NotEmpty(t, obs.Error)
		assert.Nil(t, obs.Artifact)
		require.NotNil(t, obs.PriorOutputPreserved)
		assert.True(t, *obs.PriorOutputPreserved)
		_, err := os.Stat(filepath.Join(opts.Output, fmt.Sprintf("trial-%03d", i), "tmp"))
		assert.ErrorIs(t, err, os.ErrNotExist)
		leftovers, err := filepath.Glob(filepath.Join(opts.Output, fmt.Sprintf("trial-%03d", i), ".microfat-*"))
		require.NoError(t, err)
		assert.Empty(t, leftovers)
		_, err = os.Stat(filepath.Join(opts.Output, fmt.Sprintf("trial-%03d", i), "request.json"))
		require.NoError(t, err, "cleanup must retain the request and prior output")
		_, err = os.Stat(obs.Request.Output)
		require.NoError(t, err)
	}
	last := r.Observations[len(r.Observations)-1]
	require.NotNil(t, last.Worker)
	assert.Contains(t, last.Worker.Error, pack.ErrEncoderPoolBudget.Error())
	for _, mutateOutput := range []string{"replace", "delete"} {
		t.Run(mutateOutput, func(t *testing.T) {
			opts, _ := campaignFixture(t)
			opts.Limits = true
			r, err := runCampaign(context.Background(), opts, func(
				_ context.Context, _ trialResourceConfig, _ string, args, _ []string,
			) (resourceTrialResult, error) {
				var req request
				require.NoError(t, readJSON(args[2], &req))
				if mutateOutput == "delete" {
					require.NoError(t, os.Remove(req.Output))
				} else {
					require.NoError(t, os.WriteFile(req.Output, []byte("replaced output"), 0o600))
				}
				return resourceTrialResult{ExitCode: 1}, nil
			})
			if mutateOutput == "delete" {
				require.Error(t, err)
				require.Len(t, r.Observations, 1)
			} else {
				require.NoError(t, err)
				require.Len(t, r.Observations, 5)
			}
			for _, obs := range r.Observations {
				require.NotNil(t, obs.PriorOutputPreserved)
				assert.False(t, *obs.PriorOutputPreserved, "a replacement must never be reported as preserved")
			}
		})
	}
}

func TestCampaignRuntimeControlMismatchesStopMeasurement(t *testing.T) {
	t.Parallel()
	for _, control := range []string{"toolchain", "cpu", fixtureMemory} {
		t.Run(control, func(t *testing.T) {
			t.Parallel()
			opts, _ := campaignFixture(t)
			r, err := runCampaign(context.Background(), opts, func(
				_ context.Context, cfg trialResourceConfig, _ string, _, _ []string,
			) (resourceTrialResult, error) {
				worker := workerResult{GoVersion: researchGoVersion, GOMAXPROCS: cfg.GOMAXPROCS, GoMemLimitBytes: cfg.GoMemLimitBytes}
				switch control {
				case "toolchain":
					worker.GoVersion = "go1.27.2"
				case "cpu":
					worker.GOMAXPROCS++
				case fixtureMemory:
					worker.GoMemLimitBytes++
				}
				data, err := json.Marshal(worker)
				require.NoError(t, err)
				return resourceTrialResult{Stdout: string(data)}, nil
			})
			require.ErrorContains(t, err, "runtime controls differ")
			require.Len(t, r.Observations, 1)
			assert.Nil(t, r.Observations[0].Artifact)
		})
	}
}

func TestOwnedWorkerCleanupRetainsEvidenceAndUnconfirmedWorkerState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"tmp", ".microfat-spools-owned"} {
		require.NoError(t, os.Mkdir(filepath.Join(dir, name), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name, "payload"), []byte("partial"), 0o600))
	}
	for _, name := range []string{".microfat-pack-owned.tmp", "request.json", "artifact", "unrelated", ".microfat-pack-keep"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("keep"), 0o600))
	}
	require.NoError(t, cleanupWorkerTemps(dir))
	for _, name := range []string{"tmp", ".microfat-spools-owned", ".microfat-pack-owned.tmp"} {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.ErrorIs(t, err, os.ErrNotExist)
	}
	for _, name := range []string{"request.json", "artifact", "unrelated", ".microfat-pack-keep"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.Equal(t, "keep", string(data))
	}
	require.Error(t, cleanupWorkerTemps(filepath.Join(dir, fixtureMissing)))
	opts, _ := campaignFixture(t)
	r, err := runCampaign(context.Background(), opts, func(
		_ context.Context, cfg trialResourceConfig, _ string, _, _ []string,
	) (resourceTrialResult, error) {
		require.NoError(t, os.WriteFile(filepath.Join(cfg.TrialDir, "tmp", "active-worker"), []byte("active"), 0o600))
		return resourceTrialResult{}, ErrResourceCleanup
	})
	require.ErrorIs(t, err, ErrResourceCleanup)
	require.Len(t, r.Observations, 1)
	_, err = os.Stat(filepath.Join(opts.Output, "trial-000", "tmp", "active-worker"))
	require.NoError(t, err, "an unconfirmed live worker's files must not be removed")
}

func TestCampaignPathNormalizationAndEnvelopeIOFailure(t *testing.T) {
	t.Parallel()
	opts, err := absoluteCampaignPaths(campaignOptions{Manifest: "manifest.json", Stub: "stub", Output: "output"})
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(opts.Manifest))
	assert.True(t, filepath.IsAbs(opts.Stub))
	assert.True(t, filepath.IsAbs(opts.Output))
	assert.Empty(t, opts.Minimal)
	opts, corpus := campaignFixture(t)
	req, path := fixtureWorkerRequest(t, opts, corpus, false)
	worker, err := runWorker(context.Background(), path)
	require.NoError(t, err)
	f, err := os.Open(req.Output)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.Error(t, verifyEnvelope(f, worker.Index, req), "descriptor read errors cannot pass envelope validation")
}

func fixtureTrialRunner(t *testing.T) trialRunner {
	t.Helper()
	return func(ctx context.Context, config trialResourceConfig, exe string, args, env []string) (resourceTrialResult, error) {
		require.True(t, filepath.IsAbs(exe))
		require.Len(t, args, 3)
		assert.Equal(t, []string{"--mode=worker", campaignTestRequestFlag}, args[:2])
		assert.Equal(t, config.CPUQuotaPercent, config.GOMAXPROCS*quotaScale)
		assert.Equal(t, config.MemoryMaxBytes*goMemoryNumerator/goMemoryDenominator, config.GoMemLimitBytes)
		assert.Equal(t, []string{"TMPDIR=" + filepath.Join(config.TrialDir, "tmp")}, env)
		worker, workerErr := runWorker(ctx, args[2])
		// DI models a controlled child without mutating this test process's global
		// runtime settings. The resource adapter tests own OS-control enforcement.
		worker.GOMAXPROCS, worker.GoMemLimitBytes = config.GOMAXPROCS, config.GoMemLimitBytes
		data, err := json.Marshal(worker)
		require.NoError(t, err)
		exitCode := 0
		if workerErr != nil {
			exitCode = 1
		}
		return resourceTrialResult{Stdout: string(data), ExitCode: exitCode, TelemetryAvailable: true}, nil
	}
}

func TestCampaignAndMatrixUseActualPackerThroughRunnerDI(t *testing.T) {
	t.Parallel()
	for _, matrix := range []bool{false, true} {
		t.Run(fmt.Sprintf("matrix=%t", matrix), func(t *testing.T) {
			t.Parallel()
			opts, corpus := campaignFixture(t)
			opts.Matrix = matrix
			corpus.Variants = corpus.Variants[:3]
			storeCampaignJSON(t, opts.Manifest, corpus)
			r, err := runCampaign(context.Background(), opts, fixtureTrialRunner(t))
			require.NoError(t, err)
			expected := 15
			if matrix {
				expected = 32
			}
			require.Len(t, r.Observations, expected)
			assert.Equal(t, campaignTestRevision, r.ProducerRevision)
			assert.Len(t, r.ProducerSHA256, sha256.Size*2)
			assert.Len(t, r.ManifestSHA256, sha256.Size*2)
			assert.Len(t, r.StubSHA256, sha256.Size*2)
			assert.Equal(t, matrix, r.MinimalStubSHA256 != "")
			for _, obs := range r.Observations {
				require.Empty(t, obs.Error)
				require.NotNil(t, obs.Worker)
				require.NotNil(t, obs.Artifact)
				assert.Equal(t, obs.Spec.Format, obs.Artifact.Index.Version)
				assert.Equal(t, obs.Spec.StubProfile == "minimal", obs.Request.Stub == opts.Minimal)
				assert.Equal(t, obs.Spec.Mode != "current", obs.Request.Pool != nil)
				if obs.Spec.Mode == "serial1" {
					assert.Equal(t, 1, obs.Request.Pool.MaxWorkers)
				}
			}
			var saved campaignReport
			require.NoError(t, readJSON(filepath.Join(opts.Output, "report.json"), &saved))
			expectedJSON, err := json.Marshal(r)
			require.NoError(t, err)
			actualJSON, err := json.Marshal(saved)
			require.NoError(t, err)
			assert.JSONEq(t, string(expectedJSON), string(actualJSON))
			_, err = runCampaign(context.Background(), opts, fixtureTrialRunner(t))
			require.Error(t, err, "existing output cannot be overwritten")
		})
	}
}

func TestObservationFailuresRemainInDenominatorAndSetupStops(t *testing.T) {
	t.Parallel()
	setupFailure := errors.New("mock resource setup failure")
	for _, scenario := range []string{"setup", "exit", fixtureOOM, "no-json", "truncated", "bad-json", "worker-error"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			opts, corpus := campaignFixture(t)
			corpus.Variants = corpus.Variants[:3]
			storeCampaignJSON(t, opts.Manifest, corpus)
			runner := func(context.Context, trialResourceConfig, string, []string, []string) (resourceTrialResult, error) {
				if scenario == "setup" {
					return resourceTrialResult{}, setupFailure
				}
				out := resourceTrialResult{Stdout: "{}"}
				switch scenario {
				case "exit":
					out.ExitCode = 1
				case fixtureOOM:
					out.ExitCode, out.OOMKilled = 137, true
				case "no-json":
					out.Stdout = ""
				case "truncated":
					out.OutputTruncated = true
				case "bad-json":
					out.Stdout = `{"unknown":true}`
				case "worker-error":
					out.Stdout = `{"error":"admission rejected"}`
				}
				return out, nil
			}
			r, err := runCampaign(context.Background(), opts, runner)
			if scenario == "setup" {
				require.ErrorIs(t, err, setupFailure)
				require.Len(t, r.Observations, 1)
			} else {
				require.NoError(t, err)
				require.Len(t, r.Observations, 15)
			}
			for _, obs := range r.Observations {
				assert.NotEmpty(t, obs.Error)
				assert.Nil(t, obs.Artifact)
			}
			_, statErr := os.Stat(filepath.Join(opts.Output, "report.json"))
			require.NoError(t, statErr, "failed trials are retained in a report")
		})
	}
	opts, corpus := campaignFixture(t)
	corpus.Variants, opts.Matrix = corpus.Variants[:3], true
	storeCampaignJSON(t, opts.Manifest, corpus)
	r, err := runCampaign(context.Background(), opts, func(
		context.Context, trialResourceConfig, string, []string, []string,
	) (resourceTrialResult, error) {
		return resourceTrialResult{ExitCode: 1}, nil
	})
	require.ErrorContains(t, err, "matrix contains failed observations")
	require.Len(t, r.Observations, 32)
}

type campaignErrorWriter struct{ err error }

func (w campaignErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestCampaignRunFlagsAndWorkerJSON(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--unknown"}, {"positional"}, {"--mode=unknown"}, nil} {
		var stdout, stderr bytes.Buffer
		assert.NotZero(t, run(context.Background(), args, &stdout, &stderr))
		assert.NotEmpty(t, stderr.String())
	}
	opts, corpus := campaignFixture(t)
	req, path := fixtureWorkerRequest(t, opts, corpus, true)
	var stdout, stderr bytes.Buffer
	require.Zero(t, run(context.Background(), []string{"--mode=worker", campaignTestRequestFlag, path}, &stdout, &stderr))
	require.Empty(t, stderr.String())
	var worker workerResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &worker))
	assert.NotNil(t, worker.Index)
	assert.Equal(t, req.AppName, worker.Index.AppName)
	req.Pool.MemoryBudgetBytes = 1
	storeCampaignJSON(t, path, req)
	stdout.Reset()
	assert.Equal(t, 1, run(context.Background(), []string{"--mode=worker", campaignTestRequestFlag, path}, &stdout, &stderr))
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &worker))
	assert.NotEmpty(t, worker.Error)
	for _, raw := range []string{`{"stub":"first","stub":"second"}`, `{"not_a_request":true}`} {
		require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
		_, err := runWorker(context.Background(), path)
		require.Error(t, err)
	}
	// Corpus mode exercises the CLI's successful JSON output without creating a
	// systemd resource unit. A failed stdout write is an explicit command error.
	require.NoError(t, os.Remove(opts.Manifest))
	stdout.Reset()
	require.Zero(t, run(context.Background(), []string{"--mode=corpus", "--corpus", filepath.Dir(opts.Manifest),
		"--source-revision", campaignTestRevision}, &stdout, io.Discard))
	var manifest corpusManifest
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &manifest))
	assert.Equal(t, corpus, manifest)
	require.NoError(t, os.Remove(opts.Manifest))
	writeFailure := errors.New("stdout failed")
	assert.Equal(t, 1, run(context.Background(), []string{"--mode=corpus", "--corpus", filepath.Dir(opts.Manifest),
		"--source-revision", campaignTestRevision}, campaignErrorWriter{err: writeFailure}, &stderr))
	assert.Contains(t, stderr.String(), writeFailure.Error())
}

func TestCampaignSummaryCLIReadsRetainedReportAndRejectsBadInput(t *testing.T) {
	t.Parallel()
	for _, malformed := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), fixtureMissing)
		if malformed {
			require.NoError(t, os.WriteFile(path, []byte(`{"schema_version":`), privateFileMode))
		}
		var stdout, stderr bytes.Buffer
		assert.Equal(t, 1, run(context.Background(), []string{"--mode=summary", campaignTestRequestFlag, path}, &stdout, &stderr))
		assert.Empty(t, stdout.String())
		assert.NotEmpty(t, stderr.String())
	}
	opts, corpus := campaignFixture(t)
	corpus.Variants = corpus.Variants[:3]
	storeCampaignJSON(t, opts.Manifest, corpus)
	// Retain a real campaign report with mocked child failures. No systemd unit or
	// timing experiment runs here; null successful metrics must remain null.
	report, err := runCampaign(context.Background(), opts, func(
		context.Context, trialResourceConfig, string, []string, []string,
	) (resourceTrialResult, error) {
		return resourceTrialResult{ExitCode: 1}, nil
	})
	require.NoError(t, err)
	require.Len(t, report.Observations, 15)
	var stdout, stderr bytes.Buffer
	require.Zero(t, run(context.Background(), []string{"--mode=summary", campaignTestRequestFlag, filepath.Join(opts.Output, "report.json")},
		&stdout, &stderr))
	require.Empty(t, stderr.String())
	var summary campaignSummary
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &summary, json.RejectUnknownMembers(true)))
	assert.Equal(t, report.ProducerRevision, summary.ProducerRevision)
	assert.Equal(t, report.ProducerSHA256, summary.ProducerSHA256)
	require.Len(t, summary.Groups, len(report.Observations))
	for _, group := range summary.Groups {
		assert.Equal(t, 1, group.Attempts)
		assert.Equal(t, 1, group.Failed)
		assert.Zero(t, group.Succeeded)
		assert.Nil(t, group.PackWallNS.Median)
	}
	for _, comparison := range summary.Comparisons {
		assert.Zero(t, comparison.CompletePairs)
		assert.Equal(t, 1, comparison.FailedPairs)
		assert.Nil(t, comparison.PackWallSpeedRatio.Median)
	}
	invalidReport := filepath.Join(t.TempDir(), "invalid-report.json")
	storeCampaignJSON(t, invalidReport, campaignReport{})
	stderr.Reset()
	assert.Equal(t, 1, run(context.Background(), []string{"--mode=summary", campaignTestRequestFlag, invalidReport}, io.Discard, &stderr))
	assert.Contains(t, stderr.String(), ErrSummaryInput.Error())
}

func TestCampaignSetupAndArtifactFailures(t *testing.T) {
	t.Parallel()
	for _, change := range []func(*campaignOptions){
		func(o *campaignOptions) { o.Source = "bad" },
		func(o *campaignOptions) { o.Trials = 0 },
		func(o *campaignOptions) { o.Trials = maxTrials + 1 },
		func(o *campaignOptions) { o.Output = "" },
		func(o *campaignOptions) { o.Manifest += fixtureMissing },
		func(o *campaignOptions) { o.Stub += fixtureMissing },
		func(o *campaignOptions) { o.Matrix = true; o.Minimal += fixtureMissing },
		func(o *campaignOptions) { o.Output = filepath.Join(o.Output, fixtureMissing, "output") },
	} {
		opts, _ := campaignFixture(t)
		change(&opts)
		_, err := runCampaign(context.Background(), opts, fixtureTrialRunner(t))
		require.Error(t, err)
	}
	for _, scenario := range []string{"missing-artifact", "metadata", "cancel", "report-exists"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			opts, corpus := campaignFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancel" {
				cancel()
			}
			runner := func(ctx context.Context, cfg trialResourceConfig, exe string, args, env []string) (resourceTrialResult, error) {
				if scenario == "missing-artifact" {
					return resourceTrialResult{Stdout: "{}"}, nil
				}
				if scenario == "report-exists" {
					require.NoError(t, os.WriteFile(filepath.Join(opts.Output, "report.json"), []byte("keep"), 0o600))
					return resourceTrialResult{}, errors.New("stop fixture")
				}
				var req request
				require.NoError(t, readJSON(args[2], &req))
				req.AppName = "wrong-metadata"
				storeCampaignJSON(t, args[2], req)
				return fixtureTrialRunner(t)(ctx, cfg, exe, args, env)
			}
			r, err := runCampaign(ctx, opts, runner)
			require.Error(t, err)
			if scenario == "cancel" {
				assert.Empty(t, r.Observations)
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.Len(t, r.Observations, 1)
				assert.NotEmpty(t, r.Observations[0].Error)
			}
			assert.Equal(t, len(corpus.Variants), len(r.Corpus.Variants))
		})
	}
}

func TestRunObservationSetupFailures(t *testing.T) {
	t.Parallel()
	opts, corpus := campaignFixture(t)
	spec := makeSpecs([]string{launchTestFamily}, opts)[0]
	runner := fixtureTrialRunner(t)
	// The observation owns only its new trial directory and refuses collisions.
	require.NoError(t, os.Mkdir(opts.Output, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(opts.Output, "trial-000"), []byte("keep"), 0o600))
	_, err := runObservation(context.Background(), opts, corpus, spec, 0, "/fixture", runner)
	require.Error(t, err)
	before := maps.Clone(familyVariants(corpus, launchTestFamily))
	_, err = runObservation(context.Background(), opts, corpus, spec, 1, "/fixture", runner)
	require.NoError(t, err)
	assert.Equal(t, before, familyVariants(corpus, launchTestFamily))
	data, err := os.ReadFile(filepath.Join(opts.Output, "trial-000"))
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data))
}

func TestDigestMatchesExactFileBytes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "payload")
	data := []byte("raw, exact corpus bytes")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	got, size, err := digestFile(path, maxInput)
	require.NoError(t, err)
	hash := sha256.Sum256(data)
	assert.Equal(t, hex.EncodeToString(hash[:]), got)
	assert.Equal(t, int64(len(data)), size)
}
