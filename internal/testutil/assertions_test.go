package testutil_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/testutil"
)

const (
	testPayloadLen = 1024
	testIterCount  = 10
	testFixedSeed  = 42
)

func TestAssertPayloadIntegrity(t *testing.T) {
	t.Parallel()

	raw := []byte("Hello microfat invariant test payload")
	h := sha256.Sum256(raw)
	expectedHash := hex.EncodeToString(h[:])

	c, err := codec.Get(codec.AlgorithmZstd)
	if err != nil {
		t.Fatalf("getting zstd codec: %v", err)
	}

	var compressed bytes.Buffer
	if err := c.Compress(&compressed, raw, "fastest"); err != nil {
		t.Fatalf("compressing payload: %v", err)
	}

	prefix := []byte("HEADER_PAD_BYTES_")
	binaryData := make([]byte, 0, len(prefix)+compressed.Len())
	binaryData = append(binaryData, prefix...)
	binaryData = append(binaryData, compressed.Bytes()...)

	entry := format.VariantEntry{
		Level:            "v1",
		Offset:           int64(len(prefix)),
		CompressedSize:   int64(compressed.Len()),
		UncompressedSize: int64(len(raw)),
		SHA256:           expectedHash,
		Compression:      codec.AlgorithmZstd,
	}

	testutil.AssertPayloadIntegrity(t, binaryData, entry)
}

func TestAssertDecompressionBounds(t *testing.T) {
	t.Parallel()

	c, err := codec.Get(codec.AlgorithmZstd)
	if err != nil {
		t.Fatalf("getting zstd codec: %v", err)
	}

	payload := testutil.RandomPayload(rand.New(rand.NewPCG(testFixedSeed, testFixedSeed)), testPayloadLen)
	var compressed bytes.Buffer
	if err := c.Compress(&compressed, payload, "fastest"); err != nil {
		t.Fatalf("compressing payload: %v", err)
	}

	testutil.AssertDecompressionBounds(t, c, bytes.NewReader(compressed.Bytes()), int64(len(payload)))
	testutil.AssertDecompressionBounds(t, c, bytes.NewReader([]byte("garbage_bytes_for_bounds")), int64(len(payload)))
}

func TestAssertLevelRequirements(t *testing.T) {
	t.Parallel()

	// AMD64 v1 baseline
	testutil.AssertLevelRequirements(t, microarch.ArchAMD64, microarch.AMD64v1, []string{"cx16"})

	// AMD64 v3 features
	v3Features := []string{
		"cx16", "popcnt", "sse3", "ssse3", "sse4.1", "sse4.2",
		"avx", "avx2", "bmi1", "bmi2", "fma", "osxsave", "f16c", "lzcnt", "movbe",
	}
	testutil.AssertLevelRequirements(t, microarch.ArchAMD64, microarch.AMD64v3, v3Features)

	// ARM64 v8.0 features
	arm80Features := []string{"fp", "asimd"}
	testutil.AssertLevelRequirements(t, microarch.ArchARM64, microarch.ARM64v8_0, arm80Features)
}

func TestAssertCacheIsolation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "cache")
	if err := os.MkdirAll(cacheDir, testutil.PrivatePermissionMode); err != nil {
		t.Fatalf("creating cache dir: %v", err)
	}

	childFile := filepath.Join(cacheDir, "artifact")
	if err := os.WriteFile(childFile, []byte("cached binary"), testutil.PrivatePermissionMode); err != nil {
		t.Fatalf("writing child artifact: %v", err)
	}

	testutil.AssertCacheIsolation(t, cacheDir)
}

func TestRunPropertyTest(t *testing.T) {
	t.Parallel()

	var executed atomic.Int64
	testutil.RunPropertyTest(t, "sample_property", testIterCount, testFixedSeed, func(subT *testing.T, iter int, rng *rand.Rand) {
		buf := testutil.RandomPayload(rng, 64)
		if len(buf) != 64 {
			subT.Fatalf("expected length 64, got %d", len(buf))
		}
		mutated := testutil.MutateBytes(rng, buf, 2)
		if len(mutated) != len(buf) {
			subT.Fatalf("mutated length mismatch")
		}
		truncated := testutil.TruncateBytes(rng, buf)
		if len(truncated) > len(buf) {
			subT.Fatalf("truncated length exceeds original")
		}
		executed.Add(1)
	})
}
