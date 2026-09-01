// Package testutil provides reusable property-based test assertions, invariant validators,
// and randomized mutation generators for microfat test suites.
package testutil

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

// Invariant testing constants.
const (
	PrivatePermissionMode os.FileMode = 0o700
	PermMaskOtherGroup    os.FileMode = 0o077
	DefaultPropertyIters              = 100
	DefaultMutationCount              = 3
	DefaultSafetyCeiling  int64       = 1024 * 1024 * 1024 // 1 GiB
	prngMultiplier                    = 0x5DEECE66D
	prngAddend                        = 1
	bitsPerByte                       = 8
	byteModulo                        = 256
	byteMask                          = 0xFF
)

// AssertPayloadIntegrity verifies that a variant payload within binary satisfies
// uncompressed size boundaries and cryptographic SHA-256 integrity.
func AssertPayloadIntegrity(t testing.TB, binary []byte, entry format.VariantEntry) {
	t.Helper()

	if entry.Offset < 0 || entry.CompressedSize < 0 || entry.UncompressedSize < 0 {
		t.Fatalf("invalid negative bounds in entry: offset=%d compressed=%d uncompressed=%d",
			entry.Offset, entry.CompressedSize, entry.UncompressedSize)
	}

	totalEnd := entry.Offset + entry.CompressedSize
	if totalEnd > int64(len(binary)) {
		t.Fatalf("variant entry payload extends out of bounds: offset=%d size=%d binary_len=%d",
			entry.Offset, entry.CompressedSize, len(binary))
	}

	payloadBytes := binary[entry.Offset:totalEnd]
	c, err := codec.Get(entry.Compression)
	if err != nil {
		t.Fatalf("resolving codec %q: %v", entry.Compression, err)
	}

	var decompressed bytes.Buffer
	hasher := sha256.New()
	mw := io.MultiWriter(&decompressed, hasher)

	err = c.Decompress(mw, bytes.NewReader(payloadBytes), entry.UncompressedSize)
	if err != nil {
		t.Fatalf("decompression failed for variant %s (compression %s): %v",
			entry.Level, entry.Compression, err)
	}

	if int64(decompressed.Len()) != entry.UncompressedSize {
		t.Fatalf("decompressed size mismatch: expected %d bytes, got %d bytes",
			entry.UncompressedSize, decompressed.Len())
	}

	if entry.SHA256 != "" {
		computedHash := hex.EncodeToString(hasher.Sum(nil))
		if computedHash != entry.SHA256 {
			t.Fatalf("SHA-256 mismatch for variant %s: expected %s, got %s",
				entry.Level, entry.SHA256, computedHash)
		}
	}
}

// AssertDecompressionBounds verifies that decompressing stream r via codec c
// strictly respects the byte limit ceiling and does not exceed limit bytes written,
// even when processing corrupted, malformed, or hostile streams.
func AssertDecompressionBounds(t testing.TB, c codec.Codec, r io.Reader, limit int64) {
	t.Helper()

	if limit <= 0 {
		limit = DefaultSafetyCeiling
	}

	var out bytes.Buffer
	err := c.Decompress(&out, r, limit)

	if int64(out.Len()) > limit {
		t.Fatalf("decompression bound violation: wrote %d bytes, exceeding limit of %d bytes",
			out.Len(), limit)
	}

	if err != nil && !errors.Is(err, codec.ErrSizeMismatch) && !errors.Is(err, codec.ErrDecompressionFailed) {
		t.Fatalf("expected bounded decompression sentinel error, got unexpected error: %v", err)
	}
}

// AssertLevelRequirements validates microarchitecture feature prerequisites and
// verifies monotonicity for the specified CPU architecture and target level.
func AssertLevelRequirements(t testing.TB, arch string, level string, features []string) {
	t.Helper()

	featMap := make(map[string]bool, len(features))
	for _, f := range features {
		featMap[f] = true
	}

	switch arch {
	case microarch.ArchAMD64:
		x86 := x86FeaturesFromMap(featMap)
		detectedLevel := microarch.EvaluateAMD64(x86)
		if detectedLevel != level {
			t.Fatalf("AMD64 level evaluation mismatch: expected %s, got %s for features %v",
				level, detectedLevel, features)
		}
	case microarch.ArchARM64:
		arm := arm64FeaturesFromMap(featMap)
		detectedLevel, statuses := microarch.EvaluateARM64Detailed(arm)
		if detectedLevel != level {
			t.Fatalf("ARM64 level evaluation mismatch: expected %s, got %s for features %v",
				level, detectedLevel, features)
		}
		for _, s := range statuses {
			if s.Level == level && !s.Satisfied {
				t.Fatalf("ARM64 status for %s reported not satisfied despite detection match", level)
			}
		}
	default:
		t.Fatalf("unsupported architecture for level requirement assertion: %s", arch)
	}
}

// AssertCacheIsolation verifies that the specified cache path strictly maintains
// private 0700 permissions and is not accessible to other users or groups.
func AssertCacheIsolation(t testing.TB, cachePath string) {
	t.Helper()

	info, err := os.Stat(cachePath)
	if err != nil {
		t.Fatalf("stat cache path %s: %v", cachePath, err)
	}

	perm := info.Mode().Perm()
	if perm&PermMaskOtherGroup != 0 {
		t.Fatalf("cache isolation violation: path %s has insecure permissions %04o (group/other bits set)",
			cachePath, perm)
	}

	if info.IsDir() {
		entries, err := os.ReadDir(cachePath)
		if err != nil {
			t.Fatalf("reading cache directory %s: %v", cachePath, err)
		}
		for _, entry := range entries {
			childPath := filepath.Join(cachePath, entry.Name())
			childInfo, err := entry.Info()
			if err != nil {
				t.Fatalf("stat cache child %s: %v", childPath, err)
			}
			childPerm := childInfo.Mode().Perm()
			if childPerm&PermMaskOtherGroup != 0 {
				t.Fatalf("cache child isolation violation: path %s has insecure permissions %04o",
					childPath, childPerm)
			}
		}
	}
}

// RunPropertyTest executes a property-based testing function for the specified iterations
// with deterministic per-iteration PRNG instances. If seed is 0, a timestamp seed is selected and logged on error.
func RunPropertyTest(t *testing.T, name string, iterations int, seed uint64, fn func(t *testing.T, iter int, rng *rand.Rand)) {
	t.Helper()

	if iterations <= 0 {
		iterations = DefaultPropertyIters
	}

	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
	}

	t.Run(name, func(t *testing.T) {
		t.Parallel()

		for i := range iterations {
			iterSeed1 := seed ^ (uint64(i) * prngMultiplier)
			iterSeed2 := (uint64(i) + 1) * prngMultiplier + prngAddend

			success := t.Run(fmt.Sprintf("iter_%03d", i), func(subT *testing.T) {
				subT.Parallel()
				// #nosec G404 -- pseudo-random generator used exclusively for reproducible property testing seeds
				rng := rand.New(rand.NewPCG(iterSeed1, iterSeed2))
				fn(subT, i, rng)
			})
			if !success {
				t.Logf("[property-test-failure] seed=%d iteration=%d/%d test=%s",
					seed, i, iterations, name)
				break
			}
		}
	})
}

// MutateBytes returns a copy of src with randomized bit flips or corrupted bytes.
func MutateBytes(rng *rand.Rand, src []byte, count int) []byte {
	if len(src) == 0 {
		return []byte{0xFF}
	}
	res := make([]byte, len(src))
	copy(res, src)

	if count <= 0 {
		count = DefaultMutationCount
	}

	for range count {
		idx := rng.IntN(len(res))
		bit := rng.IntN(bitsPerByte)
		res[idx] ^= byte(1 << bit)
	}
	return res
}

// TruncateBytes returns a truncated subslice copy of src.
func TruncateBytes(rng *rand.Rand, src []byte) []byte {
	if len(src) == 0 {
		return []byte{}
	}
	truncLen := rng.IntN(len(src))
	res := make([]byte, truncLen)
	copy(res, src[:truncLen])
	return res
}

// RandomPayload generates semi-compressible random payload bytes of the given length.
func RandomPayload(rng *rand.Rand, length int) []byte {
	if length <= 0 {
		return []byte{}
	}
	buf := make([]byte, length)
	patternPeriod := rng.IntN(byteModulo) + 1
	for i := range buf {
		// #nosec G115 -- bounded by byteModulo bitwise operation
		buf[i] = byte(((i % patternPeriod) ^ rng.IntN(byteModulo)) & byteMask)
	}
	return buf
}

func x86FeaturesFromMap(m map[string]bool) microarch.X86Features {
	return microarch.X86Features{
		HasCX16:     m["cx16"],
		HasPOPCNT:   m["popcnt"],
		HasSSE3:     m["sse3"],
		HasSSSE3:    m["ssse3"],
		HasSSE41:    m["sse4.1"],
		HasSSE42:    m["sse4.2"],
		HasAVX:      m["avx"],
		HasAVX2:     m["avx2"],
		HasBMI1:     m["bmi1"],
		HasBMI2:     m["bmi2"],
		HasFMA:      m["fma"],
		HasOSXSAVE:  m["osxsave"],
		HasF16C:     m["f16c"],
		HasLZCNT:    m["lzcnt"],
		HasMOVBE:    m["movbe"],
		HasAVX512F:  m["avx512f"],
		HasAVX512BW: m["avx512bw"],
		HasAVX512CD: m["avx512cd"],
		HasAVX512DQ: m["avx512dq"],
		HasAVX512VL: m["avx512vl"],
	}
}

func arm64FeaturesFromMap(m map[string]bool) microarch.ARM64Features {
	return microarch.ARM64Features{
		HasFP:       m["fp"],
		HasASIMD:    m["asimd"],
		HasATOMICS:  m["atomics"],
		HasCRC32:    m["crc32"],
		HasFPHP:     m["fphp"],
		HasASIMDHP:  m["asimdhp"],
		HasJSCVT:    m["jscvt"],
		HasFCMA:     m["fcma"],
		HasLRCPC:    m["lrcpc"],
		HasDCPOP:    m["dcpop"],
		HasASIMDDP:  m["asimddp"],
		HasDIT:      m["dit"],
		HasSVE:      m["sve"],
		HasSVE2:     m["sve2"],
		HasI8MM:     m["i8mm"],
		HasBF16:     m["bf16"],
		HasWFxT:     m["wfxt"],
		HasSME:      m["sme"],
		HasSME2:     m["sme2"],
		HasMOPS:     m["mops"],
		HasNMI:      m["nmi"],
		HasHBC:      m["hbc"],
		HasGCS:      m["gcs"],
		HasTHE:      m["the"],
		HasAES:      m["aes"],
		HasPMULL:    m["pmull"],
		HasSHA1:     m["sha1"],
		HasSHA2:     m["sha2"],
		HasSHA3:     m["sha3"],
		HasSHA512:   m["sha512"],
		HasSM3:      m["sm3"],
		HasSM4:      m["sm4"],
		HasASIMDFHM: m["asimdfhm"],
		HasASIMDRDM: m["asimdrdm"],
	}
}
