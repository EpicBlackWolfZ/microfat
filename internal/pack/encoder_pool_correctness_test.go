//go:build unix

package pack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	poolFixtureDictionaryBytes = 8 * 1024
	poolFixtureWorkspaceBytes  = 1024 * 1024
	poolFixtureMemoryBytes     = 32 * 1024 * 1024
	poolFixturePrivateMode     = 0o600
	poolFixtureTailRepeats     = 64
	poolFixtureFullStubRepeats = 128
	poolFixtureFIFO            = "fifo"
	poolFaultTimeout           = 5 * time.Second
)

type encoderPoolFixture struct {
	opts     Options
	stub     []byte
	payloads map[string][]byte
}

func newEncoderPoolFixture(t *testing.T, fullStub bool) encoderPoolFixture {
	t.Helper()
	dir := t.TempDir()
	stubPath := createDummyELF(t, dir, "stub", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
	stub, err := os.ReadFile(stubPath)
	require.NoError(t, err)
	// These are different ELF-shaped unit fixtures, not compiled runtime launchers.
	if fullStub {
		stub = append(stub, bytes.Repeat([]byte("full launcher fixture\n"), poolFixtureFullStubRepeats)...)
		require.NoError(t, os.WriteFile(stubPath, stub, 0o755))
	}
	_, source := dictionaryContractInput(t)
	opts := DefaultOptions()
	opts.StubPath, opts.OutputPath = stubPath, filepath.Join(dir, "output.fat")
	opts.AppName, opts.CompressionLevel = "encoder-pool-correctness", testCompressionFastest
	opts.DictSize = poolFixtureDictionaryBytes
	opts.Variants = make(map[string]string)
	payloads := make(map[string][]byte)
	for _, level := range []string{"v3", "v1", "v2"} {
		payload := append(slices.Clone(source), bytes.Repeat([]byte("variant-specific-tail-"+level+"\n"), poolFixtureTailRepeats)...)
		path := filepath.Join(dir, level)
		require.NoError(t, os.WriteFile(path, payload, 0o755))
		key := level
		if level == "v3" {
			key = "AMD64_V3"
		}
		opts.Variants[key], payloads[level] = path, payload
	}
	return encoderPoolFixture{opts: opts, stub: stub, payloads: payloads}
}

func correctnessPoolOptions(workers, inner int) EncoderPoolOptions {
	return EncoderPoolOptions{
		MaxWorkers: workers, InnerConcurrency: inner, CPUBudget: 4,
		MemoryBudgetBytes: poolFixtureMemoryBytes, WorkspaceBytesPerEncoder: poolFixtureWorkspaceBytes,
	}
}

func poolFixtureHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func assertEncoderPoolArtifact(t *testing.T, fixture encoderPoolFixture, idx *format.Index, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(data), len(fixture.stub))
	assert.Equal(t, fixture.stub, data[:len(fixture.stub)], "launcher bytes are copied exactly")
	verified, checks, err := VerifyBinary(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Equal(t, idx, verified)
	require.Len(t, checks, len(fixture.payloads))
	assert.Equal(t, []string{"v1", "v2", "v3"}, idx.VariantLevels(), "canonical order is independent of map enumeration")
	var dictionary []byte
	if idx.DictionarySize > 0 {
		dictionary = data[idx.DictionaryOffset : idx.DictionaryOffset+idx.DictionarySize]
		assert.Equal(t, int64(len(fixture.stub)), idx.DictionaryOffset)
		assert.Equal(t, poolFixtureHash(dictionary), idx.DictionarySHA256)
	}
	current := int64(len(fixture.stub)) + idx.DictionarySize
	for i, entry := range idx.Variants {
		require.True(t, checks[i].Valid, "%s: %s", entry.Level, checks[i].ErrorString)
		require.Equal(t, current, entry.Offset, "payloads are contiguous in canonical order")
		expected := fixture.payloads[entry.Level]
		assert.Equal(t, int64(len(expected)), entry.UncompressedSize)
		assert.Equal(t, poolFixtureHash(expected), entry.SHA256)
		decoder, err := codec.Get(entry.Compression)
		require.NoError(t, err)
		var decoded bytes.Buffer
		payload := bytes.NewReader(data[entry.Offset : entry.Offset+entry.CompressedSize])
		require.NoError(t, codec.DecompressWithOptionalDict(decoder, &decoded, payload, entry.UncompressedSize, dictionary))
		assert.Equal(t, expected, decoded.Bytes(), "every decoded byte must match the original variant")
		current += entry.CompressedSize
	}
	// Serialize the actual index independently: charge every frame, dictionary,
	// header and trailer, including variable-width Format v1 metadata.
	var metadata bytes.Buffer
	metadataSize, err := format.WriteIndexAndTrailerWithVersion(&metadata, verified, current, verified.Version)
	require.NoError(t, err)
	assert.Equal(t, int64(len(data)), current+metadataSize)
	assert.Equal(t, metadata.Bytes(), data[current:])
	return data
}

func TestEncoderPoolPackCorrectnessMatrix(t *testing.T) {
	t.Parallel()
	type compressionCase struct {
		name, algorithm string
		dictionary      bool
		mixed           bool
	}
	cases := []compressionCase{
		{name: codec.AlgorithmNone, algorithm: codec.AlgorithmNone},
		{name: "none-explicit-dictionary", algorithm: codec.AlgorithmNone, dictionary: true},
		{name: "lz4", algorithm: codec.AlgorithmLZ4},
		{name: "lz4-explicit-dictionary", algorithm: codec.AlgorithmLZ4, dictionary: true},
		{name: "zstd", algorithm: codec.AlgorithmZstd},
		{name: "zstd-dictionary", algorithm: codec.AlgorithmZstd, dictionary: true},
		{name: "mixed-codecs", algorithm: codec.AlgorithmZstd, mixed: true},
		{name: "mixed-codecs-dictionary", algorithm: codec.AlgorithmZstd, dictionary: true, mixed: true},
	}
	allocations := []struct{ workers, inner int }{{1, 1}, {2, 1}, {2, 2}, {1, 4}}
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, full := range []bool{false, true} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("v%d/full-shaped-stub=%t/%s", version, full, tc.name), func(t *testing.T) {
					t.Parallel()
					fixture := newEncoderPoolFixture(t, full)
					opts := fixture.opts
					opts.FormatVersion, opts.Compression, opts.EnableDict = version, tc.algorithm, tc.dictionary
					if tc.mixed {
						opts.VariantCompression = map[string]VariantCompressionOptions{
							"AMD64_V1": {Compression: codec.AlgorithmNone},
							"V2":       {Compression: "lz4:fastest"},
							"v3":       {Compression: "zstd:better", Level: testCompressionFastest},
						}
					}
					variantsBefore, overridesBefore := maps.Clone(opts.Variants), maps.Clone(opts.VariantCompression)
					baseline, err := Pack(opts)
					require.NoError(t, err)
					baseData := assertEncoderPoolArtifact(t, fixture, baseline, opts.OutputPath)
					for _, allocation := range allocations {
						t.Run(fmt.Sprintf("workers%d/inner%d", allocation.workers, allocation.inner), func(t *testing.T) {
							pool := correctnessPoolOptions(allocation.workers, allocation.inner)
							pooledOpts := opts
							pooledOpts.OutputPath = filepath.Join(t.TempDir(), "pooled.fat")
							idx, stats, err := PackWithEncoderPool(context.Background(), pooledOpts, pool)
							require.NoError(t, err)
							require.NotNil(t, stats)
							data := assertEncoderPoolArtifact(t, fixture, idx, pooledOpts.OutputPath)
							assert.Equal(t, len(fixture.payloads), stats.JobsTotal)
							assert.Equal(t, stats.JobsTotal, stats.JobsStarted)
							assert.Equal(t, stats.JobsTotal, stats.JobsCompleted)
							assert.Zero(t, stats.ActiveWorkers)
							assert.Zero(t, stats.AdmittedBytes)
							assert.Positive(t, stats.PeakActiveWorkers)
							assert.LessOrEqual(t, stats.PeakActiveWorkers, allocation.workers)
							assert.LessOrEqual(t, stats.PeakAdmittedBytes, pool.MemoryBudgetBytes)
							assert.Equal(t, int64(len(fixture.stub))+idx.DictionarySize, stats.SharedBytes)
							assert.Equal(t, tc.dictionary, idx.DictionarySize > 0)
							assert.Equal(t, baseline.DictionarySHA256, idx.DictionarySHA256)
							left, right := *baseline, *idx
							left.CreatedUnix, right.CreatedUnix = 0, 0
							if allocation.inner == runtime.GOMAXPROCS(0) {
								assert.Equal(t, left, right, "matched effective codec settings preserve the complete normalized index")
								assert.Equal(t, len(baseData), len(data))
							}
							for i, entry := range idx.Variants {
								assert.Equal(t, baseline.Variants[i].Compression, entry.Compression)
								assert.Equal(t, baseline.Variants[i].SHA256, entry.SHA256)
								baseEntry := baseline.Variants[i]
								baseFrame := baseData[baseEntry.Offset : baseEntry.Offset+baseEntry.CompressedSize]
								frame := data[entry.Offset : entry.Offset+entry.CompressedSize]
								t.Logf("%s frame_equal=%t baseline_bytes=%d pool_bytes=%d", entry.Level,
									bytes.Equal(baseFrame, frame), len(baseFrame), len(frame))
							}
							assert.Equal(t, variantsBefore, opts.Variants)
							assert.Equal(t, overridesBefore, opts.VariantCompression)
						})
					}
				})
			}
		}
	}
}

func TestEncoderPoolAutomaticDictionaryPolicy(t *testing.T) {
	t.Parallel()
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, consumer := range []bool{false, true} {
			t.Run(fmt.Sprintf("v%d/zstd-consumer=%t", version, consumer), func(t *testing.T) {
				t.Parallel()
				fixture := newEncoderPoolFixture(t, false)
				opts := fixture.opts
				opts.FormatVersion, opts.Profile = version, codec.ProfileSize
				opts.Compression = codec.AlgorithmNone
				opts.VariantCompression = map[string]VariantCompressionOptions{
					"v1": {Compression: codec.AlgorithmNone}, "V2": {Compression: codec.AlgorithmLZ4},
				}
				if consumer {
					opts.VariantCompression["AMD64_V3"] = VariantCompressionOptions{Compression: codec.AlgorithmZstd}
				}
				baseline, err := Pack(opts)
				require.NoError(t, err)
				assert.Equal(t, consumer, baseline.DictionarySize > 0)
				opts.OutputPath = filepath.Join(t.TempDir(), "pooled.fat")
				idx, _, err := PackWithEncoderPool(context.Background(), opts, correctnessPoolOptions(2, 1))
				require.NoError(t, err)
				assertEncoderPoolArtifact(t, fixture, idx, opts.OutputPath)
				assert.Equal(t, consumer, idx.DictionarySize > 0)
				assert.Equal(t, baseline.DictionarySHA256, idx.DictionarySHA256)
			})
		}
	}
}

func assertPoolFailurePreservesOutput(t *testing.T, fixture encoderPoolFixture, err error) {
	t.Helper()
	require.Error(t, err)
	data, readErr := os.ReadFile(fixture.opts.OutputPath)
	require.NoError(t, readErr)
	assert.Equal(t, "previous verified output", string(data))
	stat, statErr := os.Stat(fixture.opts.OutputPath)
	require.NoError(t, statErr)
	assert.Equal(t, os.FileMode(poolFixturePrivateMode), stat.Mode().Perm())
	staging, globErr := filepath.Glob(filepath.Join(filepath.Dir(fixture.opts.OutputPath), ".microfat-pack-*"))
	require.NoError(t, globErr)
	assert.Empty(t, staging, "failed jobs must not leave assembly files")
}

type poolFailingWriter struct{ err error }

func (w poolFailingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestEncoderPoolFailureCleanupAndOldOutput(t *testing.T) {
	t.Parallel()
	stageFailure := errors.New("injected encoder stage failure")
	for _, fault := range []string{"before-job", "create-spool", "write-spool", "cleanup", "late-cancel"} {
		for _, algorithm := range []string{codec.AlgorithmNone, codec.AlgorithmLZ4, codec.AlgorithmZstd} {
			t.Run(fault+"/"+algorithm, func(t *testing.T) {
				t.Parallel()
				fixture := newEncoderPoolFixture(t, false)
				fixture.opts.Compression = algorithm
				require.NoError(t, os.WriteFile(fixture.opts.OutputPath, []byte("previous verified output"), poolFixturePrivateMode))
				ctx, cancel := context.WithTimeout(context.Background(), poolFaultTimeout)
				defer cancel()
				var removed string
				hooks := encoderPoolHooks{removeSpools: func(dir string) error {
					removed = dir
					err := os.RemoveAll(dir)
					if fault == "cleanup" {
						return errors.Join(err, stageFailure)
					}
					if fault == "late-cancel" {
						cancel()
					}
					return err
				}}
				switch fault {
				case "before-job":
					hooks.beforeJob = func(context.Context, string, string) error { return stageFailure }
				case "create-spool":
					hooks.createSpool = func(string, string) (*os.File, error) { return nil, stageFailure }
				case "write-spool":
					hooks.spoolWriter = func(*os.File) io.Writer { return poolFailingWriter{err: stageFailure} }
				}
				_, stats, err := packWithEncoderPool(ctx, fixture.opts, correctnessPoolOptions(2, 1), hooks)
				assertPoolFailurePreservesOutput(t, fixture, err)
				if fault == "late-cancel" {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.ErrorIs(t, err, stageFailure)
				}
				require.NotNil(t, stats)
				assert.Zero(t, stats.ActiveWorkers)
				assert.Zero(t, stats.AdmittedBytes)
				require.NotEmpty(t, removed)
				_, statErr := os.Stat(removed)
				assert.ErrorIs(t, statErr, os.ErrNotExist, "all private spool files are cleaned")
			})
		}
	}
}

func TestEncoderPoolFailureWaitsForActivePeer(t *testing.T) {
	t.Parallel()
	fixture := newEncoderPoolFixture(t, false)
	fixture.opts.Compression = codec.AlgorithmNone
	require.NoError(t, os.WriteFile(fixture.opts.OutputPath, []byte("previous verified output"), poolFixturePrivateMode))
	ctx, cancel := context.WithTimeout(context.Background(), poolFaultTimeout)
	defer cancel()
	peerStarted, peerExited := make(chan struct{}), make(chan struct{})
	failure := errors.New("fail after the other worker starts")
	hooks := encoderPoolHooks{beforeJob: func(ctx context.Context, level, _ string) error {
		switch format.NormalizeVariant(level) {
		case "v1":
			select {
			case <-peerStarted:
				return failure
			case <-ctx.Done():
				return ctx.Err()
			}
		case "v2":
			close(peerStarted)
			<-ctx.Done()
			close(peerExited)
			return ctx.Err()
		default:
			return nil
		}
	}}
	_, stats, err := packWithEncoderPool(ctx, fixture.opts, correctnessPoolOptions(2, 1), hooks)
	require.ErrorIs(t, err, failure)
	assertPoolFailurePreservesOutput(t, fixture, err)
	select {
	case <-peerExited:
	default:
		t.Fatal("pool returned while the admitted peer was still active")
	}
	require.NotNil(t, stats)
	assert.Zero(t, stats.ActiveWorkers)
	assert.Zero(t, stats.AdmittedBytes)
}

func TestEncoderPoolCanonicalAssemblyAfterOutOfOrderCompletion(t *testing.T) {
	t.Parallel()
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			t.Parallel()
			fixture := newEncoderPoolFixture(t, false)
			fixture.opts.FormatVersion, fixture.opts.Compression = version, codec.AlgorithmNone
			thirdStarted := make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), poolFaultTimeout)
			defer cancel()
			hooks := encoderPoolHooks{beforeJob: func(ctx context.Context, level, _ string) error {
				switch format.NormalizeVariant(level) {
				case "v1":
					select {
					case <-thirdStarted:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				case "v3":
					// With two slots and v1 blocked, v3 cannot be admitted until
					// v2 has completed. This forces noncanonical completion.
					close(thirdStarted)
				}
				return nil
			}}
			idx, stats, err := packWithEncoderPool(ctx, fixture.opts, correctnessPoolOptions(2, 1), hooks)
			require.NoError(t, err)
			assert.Equal(t, 2, stats.PeakActiveWorkers)
			assertEncoderPoolArtifact(t, fixture, idx, fixture.opts.OutputPath)
		})
	}
}

func TestEncoderPoolSnapshotReplacementGrowthAndDeletion(t *testing.T) {
	t.Parallel()
	for _, mutation := range []string{"replace", "grow", "delete"} {
		for _, pooled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/pool=%t", mutation, pooled), func(t *testing.T) {
				t.Parallel()
				fixture := newEncoderPoolFixture(t, true)
				fixture.opts.Compression = codec.AlgorithmZstd
				fixture.opts.EnableDict = true
				var stubMutation sync.Once
				fixture.opts.VariantValidator = func(level, path string) error {
					snapshot, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					if !bytes.Equal(snapshot, fixture.payloads[format.NormalizeVariant(level)]) {
						return errors.New("validator did not receive the pinned original bytes")
					}
					stubMutation.Do(func() {
						err = os.WriteFile(fixture.opts.StubPath, []byte("changed original stub after its snapshot"), 0o755)
					})
					if err != nil {
						return err
					}
					original := fixture.opts.Variants[level]
					switch mutation {
					case "replace":
						replacement := original + ".replacement"
						if err := os.WriteFile(replacement, []byte("different inode and original content"), 0o755); err != nil {
							return err
						}
						return os.Rename(replacement, original)
					case "grow":
						grown := append(slices.Clone(snapshot), []byte("bytes appended after snapshot")...)
						return os.WriteFile(original, grown, 0o755)
					default:
						return os.Remove(original)
					}
				}
				var idx *format.Index
				var err error
				if pooled {
					idx, _, err = PackWithEncoderPool(context.Background(), fixture.opts, correctnessPoolOptions(2, 1))
				} else {
					idx, err = Pack(fixture.opts)
				}
				require.NoError(t, err)
				assertEncoderPoolArtifact(t, fixture, idx, fixture.opts.OutputPath)
			})
		}
	}
}

func TestEncoderPoolInputAndCancellationFailures(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"pre-cancel", "validator-cancel", "validator-error", "missing", "oversized", poolFixtureFIFO} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			fixture := newEncoderPoolFixture(t, false)
			require.NoError(t, os.WriteFile(fixture.opts.OutputPath, []byte("previous verified output"), poolFixturePrivateMode))
			ctx, cancel := context.WithTimeout(context.Background(), poolFaultTimeout)
			defer cancel()
			want := errors.New("validator failure")
			var fifo string
			switch failure {
			case "pre-cancel":
				cancel()
				want = context.Canceled
			case "validator-cancel":
				fixture.opts.VariantValidator = func(string, string) error { cancel(); return nil }
				want = context.Canceled
			case "validator-error":
				fixture.opts.VariantValidator = func(string, string) error { return want }
			case "missing":
				fixture.opts.Variants["v1"] += "-missing"
				want = os.ErrNotExist
			case "oversized":
				file, err := os.Create(fixture.opts.Variants["v1"])
				require.NoError(t, err)
				require.NoError(t, file.Truncate(format.MaxPayloadSize+1))
				require.NoError(t, file.Close())
				want = format.ErrPayloadTooLarge
			case poolFixtureFIFO:
				fifo = filepath.Join(t.TempDir(), poolFixtureFIFO)
				require.NoError(t, unix.Mkfifo(fifo, poolFixturePrivateMode))
				fixture.opts.Variants["v1"], want = fifo, ErrInvalidELF
			}
			done := make(chan error, 1)
			go func() {
				_, _, err := PackWithEncoderPool(ctx, fixture.opts, correctnessPoolOptions(2, 1))
				done <- err
			}()
			select {
			case err := <-done:
				require.ErrorIs(t, err, want)
				assertPoolFailurePreservesOutput(t, fixture, err)
			case <-time.After(poolFaultTimeout):
				if fifo != "" {
					writer, err := os.OpenFile(fifo, os.O_WRONLY|unix.O_NONBLOCK, 0)
					if err == nil {
						_ = writer.Close()
					}
				}
				t.Fatal("packing failed to return within the fault-test bound")
			}
		})
	}
}

func TestEncoderPoolSnapshotDriftAfterAdmission(t *testing.T) {
	t.Parallel()
	for _, drift := range []string{"growth", "shrink", "delete", poolFixtureFIFO} {
		t.Run(drift, func(t *testing.T) {
			t.Parallel()
			fixture := newEncoderPoolFixture(t, false)
			fixture.opts.Compression = codec.AlgorithmNone
			require.NoError(t, os.WriteFile(fixture.opts.OutputPath, []byte("previous verified output"), poolFixturePrivateMode))
			want := format.ErrPayloadTooLarge
			switch drift {
			case "shrink":
				want = ErrSizeMismatch
			case "delete":
				want = os.ErrNotExist
			case poolFixtureFIFO:
				want = ErrInvalidELF
			}
			var removed string
			hooks := encoderPoolHooks{
				beforeJob: func(_ context.Context, level, path string) error {
					if format.NormalizeVariant(level) != "v1" {
						return nil
					}
					switch drift {
					case "growth":
						grown := append(slices.Clone(fixture.payloads["v1"]), []byte("growth after admission")...)
						return os.WriteFile(path, grown, poolFixturePrivateMode)
					case "shrink":
						return os.Truncate(path, int64(len(fixture.payloads["v1"])-1))
					case poolFixtureFIFO:
						if err := os.Remove(path); err != nil {
							return err
						}
						return unix.Mkfifo(path, poolFixturePrivateMode)
					default:
						return os.Remove(path)
					}
				},
				removeSpools: func(path string) error { removed = path; return os.RemoveAll(path) },
			}
			ctx, cancel := context.WithTimeout(context.Background(), poolFaultTimeout)
			defer cancel()
			_, stats, err := packWithEncoderPool(ctx, fixture.opts, correctnessPoolOptions(2, 1), hooks)
			require.ErrorIs(t, err, want)
			assertPoolFailurePreservesOutput(t, fixture, err)
			assert.Zero(t, stats.ActiveWorkers)
			assert.Zero(t, stats.AdmittedBytes)
			require.NotEmpty(t, removed)
			_, statErr := os.Stat(removed)
			assert.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}
