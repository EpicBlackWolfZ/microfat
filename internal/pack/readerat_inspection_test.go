package pack

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testLargeELFBytes       = 8 * 1024 * 1024
	testMaxMetadataReadFrac = 256 * 1024
	testLongWritableSegment = 160 * 1024
	testBoundaryOffset      = 65520
	testSecondWindowOffset  = 96000
	testFixedCreatedUnix    = 1761400000
	testShortReadLimit      = 8
	testGrowSmallLimit      = 32
	testGrowInitialBytes    = 16
	testGrowWithinLimit     = 24
	testGrowExceedLimit     = 48
	readerAtAllocHelperEnv  = "MICROFAT_TEST_READERAT_ALLOC_PATH"
)

type countingReaderAt struct {
	data      []byte
	bytesRead atomic.Uint64
	calls     atomic.Uint64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	c.calls.Add(1)
	if off < 0 || off >= int64(len(c.data)) {
		return 0, io.EOF
	}
	n := copy(p, c.data[off:])
	c.bytesRead.Add(uint64(n)) // #nosec G115 -- copy returns non-negative count <= len(p).
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

type shortReaderAt struct {
	limit int
}

func (s shortReaderAt) ReadAt(p []byte, _ int64) (int, error) {
	if len(p) <= s.limit {
		return len(p), io.EOF
	}
	return s.limit, io.EOF
}

type failingReaderAt struct {
	data       []byte
	failAtCall int
	calls      int
}

type changingELFHeaderReaderAt struct {
	data   []byte
	mutate func([]byte)
}

func (r *changingELFHeaderReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := bytes.NewReader(r.data).ReadAt(p, off)
	if off == 0 {
		r.mutate(r.data)
	}
	return n, err
}

func TestExecutableELFReaderAtRetainsValidatedHeader(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func([]byte)
	}{
		{
			name: "program_entry_size_changes_to_zero",
			mutate: func(data []byte) {
				binary.LittleEndian.PutUint16(data[elfEhdrPhentsizeOff:elfEhdrPhentsizeEnd], 0)
			},
		},
		{
			name: "program_count_changes_beyond_budget",
			mutate: func(data []byte) {
				binary.LittleEndian.PutUint16(data[elfEhdrPhnumOff:elfEhdrPhnumEnd], math.MaxUint16)
			},
		},
		{
			name: "program_offset_changes_beyond_file",
			mutate: func(data []byte) {
				binary.LittleEndian.PutUint64(data[elfEhdrPhoffOff:elfEhdrPhoffEnd], uint64(len(data)))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, data := compressionContractInput(t)
			expected, err := readExecutableELF(data)
			require.NoError(t, err)

			// Model a file changing after its header was read. Parsing must use
			// the validated header, never a second unvalidated set of bounds.
			reader := &changingELFHeaderReaderAt{data: bytes.Clone(data), mutate: tc.mutate}
			actual, err := readExecutableELFReaderAt(reader, uint64(len(data)))
			require.NoError(t, err)
			assert.Equal(t, expected, actual)
		})
	}
}

func (f *failingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	f.calls++
	if f.calls >= f.failAtCall {
		return 0, io.ErrUnexpectedEOF
	}
	if off < 0 || off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func TestReaderAtELFAndABIInspectionBoundedBytes(t *testing.T) {
	t.Parallel()

	base := buildSyntheticELF(t, syntheticELFOpts{
		interp:       testLdLinux,
		dependencies: []string{testLibc, "libpthread.so.0"},
	})
	padded := make([]byte, testLargeELFBytes)
	copy(padded, base)
	// Expand PT_LOAD[0] file/mem size to cover the 8 MiB payload so it models a multi-MiB dynamic binary.
	binary.LittleEndian.PutUint64(padded[64+32:64+40], uint64(len(padded)))
	binary.LittleEndian.PutUint64(padded[64+40:64+48], uint64(len(padded)))

	spy := &countingReaderAt{data: padded}
	elfFile, err := readExecutableELFReaderAt(spy, uint64(len(padded)))
	require.NoError(t, err)
	require.NoError(t, validateExecutableELF(elfFile, uint64(len(padded))))
	require.NoError(t, validateELFPlatformReaderAt(spy, uint64(len(padded)), elfFile, testOSLinux))
	require.NoError(t, validateELFPlatform(padded, elfFile, testOSLinux))

	repStream, err := InspectELFABIReaderAt(spy, uint64(len(padded)))
	require.NoError(t, err)
	repSlice, err := InspectELFABI(padded)
	require.NoError(t, err)
	assert.Equal(t, repSlice, repStream)

	readBytes := spy.bytesRead.Load()
	t.Logf("8 MiB dynamic ELF inspected in %d ReadAt calls (%d bytes read)", spy.calls.Load(), readBytes)
	assert.Less(t, readBytes, uint64(testMaxMetadataReadFrac),
		"ReaderAt ELF+ABI inspection must read only bounded headers/tables, not multi-MiB code/rodata")
}

func TestGoBuildInfoSlidingWindowBoundaries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		infoOff   int
		osSetting string
		wantErr   bool
	}{
		{name: "straddling_64k_window_boundary_linux", infoOff: testBoundaryOffset, osSetting: testOSLinux, wantErr: false},
		{name: "straddling_64k_window_boundary_freebsd", infoOff: testBoundaryOffset, osSetting: platformEdgeOSFreeBSD, wantErr: true},
		{name: "second_64k_window_freebsd", infoOff: testSecondWindowOffset, osSetting: platformEdgeOSFreeBSD, wantErr: true},
		{name: "no_buildinfo_in_large_writable_segment", infoOff: -1, osSetting: "", wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := inlinePlatformELF(t, "path\tapp\nbuild\tGOOS=linux\n")
			var marker []byte
			if tc.infoOff >= 0 {
				src := inlinePlatformELF(t, "path\tapp\nbuild\tGOOS="+tc.osSetting+"\n")
				marker = bytes.Clone(src[platformInfoOffset:])
			}
			// Clear the early buildinfo at platformInfoOffset so scanning must advance across windows.
			clear(base[platformInfoOffset:])
			if len(base) < testLongWritableSegment {
				base = append(base, make([]byte, testLongWritableSegment-len(base))...)
			}
			if tc.infoOff >= 0 {
				copy(base[tc.infoOff:], marker)
			}
			binary.LittleEndian.PutUint64(base[96:104], uint64(len(base)))
			binary.LittleEndian.PutUint64(base[104:112], uint64(len(base)))

			spy := &countingReaderAt{data: base}
			elfFile, err := readExecutableELFReaderAt(spy, uint64(len(base)))
			require.NoError(t, err)
			err = validateELFPlatformReaderAt(spy, uint64(len(base)), elfFile, testOSLinux)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "GOOS")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestReadIntoAtExactAtAndBoundedInputEdgeCases(t *testing.T) {
	t.Parallel()

	// Zero-size reads succeed without touching underlying ReaderAt.
	require.NoError(t, readIntoAt(shortReaderAt{limit: 0}, nil, 0))
	empty, err := readExactAt(shortReaderAt{limit: 0}, 0, 0)
	require.NoError(t, err)
	assert.Empty(t, empty)

	// Exact-size read returning (len(buf), io.EOF) succeeds.
	exact, err := readExactAt(shortReaderAt{limit: testShortReadLimit}, 0, testShortReadLimit)
	require.NoError(t, err)
	assert.Len(t, exact, testShortReadLimit)

	// Short read fails closed with ErrABIMetadata.
	_, err = readExactAt(shortReaderAt{limit: testShortReadLimit / 2}, 0, testShortReadLimit)
	require.ErrorIs(t, err, ErrABIMetadata)

	// Signed int64 overflow in offset or size fails closed with ErrABIMetadata.
	_, err = readExactAt(shortReaderAt{limit: testShortReadLimit}, uint64(math.MaxInt64)+1, 1)
	require.ErrorIs(t, err, ErrABIMetadata)
	err = readIntoAt(shortReaderAt{limit: testShortReadLimit}, make([]byte, testShortReadLimit), uint64(math.MaxInt64))
	require.ErrorIs(t, err, ErrABIMetadata)

	// Invalid openBoundedReaderAt limits reject fail-fast.
	tmpFile := filepath.Join(t.TempDir(), "sample.bin")
	require.NoError(t, os.WriteFile(tmpFile, bytes.Repeat([]byte("x"), testGrowInitialBytes), inputSnapshotMode))
	_, _, err = openBoundedReaderAt(tmpFile, 0)
	require.ErrorIs(t, err, format.ErrPayloadTooLarge)

	// verifyBoundedReaderAtUnchanged detects concurrent growth within and beyond limit, and shrinkage.
	f, size, err := openBoundedReaderAt(tmpFile, testGrowSmallLimit)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	require.NoError(t, verifyBoundedReaderAtUnchanged(f, size, testGrowSmallLimit))

	require.NoError(t, os.WriteFile(tmpFile, bytes.Repeat([]byte("x"), testGrowWithinLimit), inputSnapshotMode))
	require.ErrorIs(t, verifyBoundedReaderAtUnchanged(f, size, testGrowSmallLimit), ErrSizeMismatch)

	require.NoError(t, os.WriteFile(tmpFile, bytes.Repeat([]byte("x"), testGrowExceedLimit), inputSnapshotMode))
	require.ErrorIs(t, verifyBoundedReaderAtUnchanged(f, size, testGrowSmallLimit), format.ErrPayloadTooLarge)

	// Closed file descriptor stat/read errors and concurrent shrink/grow in readBoundedFromFile.
	mutPath := filepath.Join(t.TempDir(), "mut.bin")
	require.NoError(t, os.WriteFile(mutPath, bytes.Repeat([]byte("a"), testGrowInitialBytes), inputSnapshotMode))
	mutF, mutSize, err := openBoundedReaderAt(mutPath, testGrowSmallLimit)
	require.NoError(t, err)

	// 1. Concurrent shrink before readBoundedFromFile -> ErrSizeMismatch.
	require.NoError(t, os.Truncate(mutPath, testShortReadLimit))
	_, err = readBoundedFromFile(mutF, mutSize, testGrowSmallLimit)
	require.ErrorIs(t, err, ErrSizeMismatch)

	// 2. Concurrent growth within limit -> ErrSizeMismatch.
	_, err = mutF.Seek(0, io.SeekStart)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(mutPath, bytes.Repeat([]byte("b"), testGrowWithinLimit), inputSnapshotMode))
	mutF2, _, err := openBoundedReaderAt(mutPath, testGrowSmallLimit)
	require.NoError(t, err)
	_, err = readBoundedFromFile(mutF2, mutSize, testGrowSmallLimit)
	require.ErrorIs(t, err, ErrSizeMismatch)
	require.NoError(t, mutF2.Close())

	// 3. Concurrent growth exceeding limit (stat.Size() > limit) -> format.ErrPayloadTooLarge.
	require.NoError(t, os.WriteFile(mutPath, bytes.Repeat([]byte("c"), testGrowExceedLimit), inputSnapshotMode))
	mutF3, err := os.Open(mutPath)
	require.NoError(t, err)
	_, err = readBoundedFromFile(mutF3, mutSize, testGrowSmallLimit)
	require.ErrorIs(t, err, format.ErrPayloadTooLarge)

	// 4. Exact-limit read with extra byte exceeding limit directly.
	_, err = mutF3.Seek(0, io.SeekStart)
	require.NoError(t, err)
	_, err = readBoundedFromFile(mutF3, testGrowSmallLimit, testGrowSmallLimit)
	require.ErrorIs(t, err, format.ErrPayloadTooLarge)
	require.NoError(t, mutF3.Close())

	// 5. Closed descriptor read/stat failure paths.
	require.NoError(t, mutF.Close())
	_, err = readBoundedFromFile(mutF, mutSize, testGrowSmallLimit)
	require.Error(t, err)
	require.Error(t, verifyBoundedReaderAtUnchanged(mutF, mutSize, testGrowSmallLimit))

	// 6. Empty file and missing file in openBoundedReaderAt / writeStubBinaryFromPath.
	emptyPath := filepath.Join(t.TempDir(), "empty.bin")
	require.NoError(t, os.WriteFile(emptyPath, nil, inputSnapshotMode))
	_, _, err = openBoundedReaderAt(emptyPath, testGrowSmallLimit)
	require.ErrorIs(t, err, ErrInvalidELF)

	_, err = writeStubBinaryFromPath(mutF, filepath.Join(t.TempDir(), "missing-stub"))
	require.ErrorIs(t, err, ErrStubMissing)
	_, err = writeStubBinaryFromPath(mutF, tmpFile)
	require.Error(t, err)
}

func TestReaderAtFaultInjectionAcrossELFPlatformAndABI(t *testing.T) {
	t.Parallel()

	const maxFaultCalls = 16

	// 1. Dynamic ELF with PT_INTERP, PT_DYNAMIC, DT_NEEDED, Verneed/Vernaux, and extended section headers.
	dynELF := buildSyntheticELF(t, syntheticELFOpts{
		interp:        testLdLinux,
		dependencies:  []string{testLibc},
		verneeds:      []syntheticVerneed{{file: testLibc, versions: []syntheticVernaux{{name: "GLIBC_2.34"}}}},
		extendedShnum: 2,
	})
	for callIdx := 1; callIdx <= maxFaultCalls; callIdx++ {
		fr := &failingReaderAt{data: dynELF, failAtCall: callIdx}
		_, _ = InspectELFABIReaderAt(fr, uint64(len(dynELF)))

		fr2 := &failingReaderAt{data: dynELF, failAtCall: callIdx}
		_, _ = readExecutableELFReaderAt(fr2, uint64(len(dynELF)))
	}

	// 2. Inline and legacy-pointer Go buildinfo ELFs under sequential ReadAt failures.
	inlineELF := inlinePlatformELF(t, "path\tapp\nbuild\tGOOS=linux\n")
	legacyELF := legacyPlatformELF(t, goBuildInfoLargePtr, binary.LittleEndian, testOSLinux)
	for _, sample := range [][]byte{inlineELF, legacyELF} {
		parsed, err := readExecutableELF(sample)
		require.NoError(t, err)
		for callIdx := 1; callIdx <= maxFaultCalls; callIdx++ {
			fr := &failingReaderAt{data: sample, failAtCall: callIdx}
			_ = validateELFPlatformReaderAt(fr, uint64(len(sample)), parsed, testOSLinux)
		}
	}
}

func TestPackReaderAtDeterministicEquivalenceAndLowAllocation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stubPath := createDummyELF(t, dir, "launcher-stub", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
	v1Path := createDummyELF(t, dir, "variant-v1-payload", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
	v3Path := createDummyELF(t, dir, "variant-v3-payload", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
	epoch := int64(testFixedCreatedUnix)

	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, profile := range []string{codec.ProfileLatency, codec.ProfileBalanced, codec.ProfileSize} {
			outA := filepath.Join(dir, "out-a")
			outB := filepath.Join(dir, "out-b")
			optsA := DefaultOptions()
			optsA.StubPath = stubPath
			optsA.OutputPath = outA
			optsA.Profile = profile
			optsA.FormatVersion = version
			optsA.CreatedUnix = &epoch
			optsA.Variants = map[string]string{"v1": v1Path, "v3": v3Path}

			optsB := optsA
			optsB.OutputPath = outB
			optsB.Variants = map[string]string{"v1": v1Path, "v3": v3Path}

			_, err := Pack(optsA)
			require.NoError(t, err)
			_, err = Pack(optsB)
			require.NoError(t, err)

			bytesA, err := os.ReadFile(outA)
			require.NoError(t, err)
			bytesB, err := os.ReadFile(outB)
			require.NoError(t, err)
			assert.Equal(t, bytesA, bytesB)

			_, results, verifyErr := VerifyBinary(bytes.NewReader(bytesA), int64(len(bytesA)))
			require.NoError(t, verifyErr)
			for _, res := range results {
				assert.True(t, res.Valid)
			}
		}
	}

	// Verify ValidateELFBinary on an 8 MiB file allocates well under 512 KiB in an isolated child process.
	largePath := filepath.Join(dir, "large-8mib.elf")
	rawBase, err := os.ReadFile(v1Path)
	require.NoError(t, err)
	padded := make([]byte, testLargeELFBytes)
	copy(padded, rawBase)
	binary.LittleEndian.PutUint64(padded[96:104], uint64(len(padded)))
	binary.LittleEndian.PutUint64(padded[104:112], uint64(len(padded)))
	require.NoError(t, os.WriteFile(largePath, padded, inputSnapshotMode))

	testExec, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), elfBudgetAllocationTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, testExec, "-test.run=^TestReaderAtLargeELFAllocationHelper$", "-test.v")
	cmd.Env = append(os.Environ(), readerAtAllocHelperEnv+"="+largePath)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "isolated ReaderAt allocation helper failed:\n%s", out)
}

func TestReaderAtLargeELFAllocationHelper(t *testing.T) {
	largePath := os.Getenv(readerAtAllocHelperEnv)
	if largePath == "" {
		t.Skip("allocation measurement runs only in the isolated helper process")
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	require.NoError(t, ValidateELFBinary(largePath, testOSLinux, testArchAMD64))
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	require.Less(t, allocated, uint64(testMaxMetadataReadFrac)*2,
		"ValidateELFBinary on 8 MiB ELF allocated %d bytes", allocated)
}

func TestOpenBoundedReaderAtAndHelperEdgeCases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	validPath, raw := compressionContractInput(t)

	_, _, err := openBoundedReaderAt(validPath, 0)
	require.ErrorIs(t, err, format.ErrPayloadTooLarge)

	_, _, err = openBoundedReaderAt(validPath, format.MaxPayloadSize+1)
	require.ErrorIs(t, err, format.ErrPayloadTooLarge)

	_, _, err = openBoundedReaderAt(validPath, testShortReadLimit)
	require.ErrorIs(t, err, format.ErrPayloadTooLarge)

	emptyPath := filepath.Join(dir, "empty.elf")
	require.NoError(t, os.WriteFile(emptyPath, nil, inputSnapshotMode))
	_, _, err = openBoundedReaderAt(emptyPath, format.MaxPayloadSize)
	require.ErrorIs(t, err, ErrInvalidELF)

	missingPath := filepath.Join(dir, "missing.elf")
	_, _, err = openBoundedReaderAt(missingPath, format.MaxPayloadSize)
	require.Error(t, err)

	tmpOut, err := os.CreateTemp(dir, "stub-out-*")
	require.NoError(t, err)
	defer func() { _ = tmpOut.Close() }()
	_, err = writeStubBinaryFromPath(tmpOut, missingPath)
	require.ErrorIs(t, err, ErrStubMissing)

	_, err = inspectVariantABIFile("v1", missingPath, NewArtifactMetadataAccounting(0))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading snapshotted variant v1")

	badELFPath := filepath.Join(dir, "bad.elf")
	require.NoError(t, os.WriteFile(badELFPath, []byte("not-an-elf-binary"), inputSnapshotMode))
	_, err = inspectVariantABIFile("v1", badELFPath, NewArtifactMetadataAccounting(0))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting declared ABI for variant v1")

	noPhdr := append([]byte(nil), raw...)
	binary.LittleEndian.PutUint64(noPhdr[elfEhdrPhoffOff:elfEhdrPhoffEnd], 0)
	binary.LittleEndian.PutUint16(noPhdr[elfEhdrPhentsizeOff:elfEhdrPhentsizeEnd], 0)
	binary.LittleEndian.PutUint16(noPhdr[elfEhdrPhnumOff:elfEhdrPhnumEnd], 0)
	parsedNoPhdr, err := readExecutableELFReaderAt(bytes.NewReader(noPhdr), uint64(len(noPhdr)))
	require.NoError(t, err)
	assert.Empty(t, parsedNoPhdr.Progs)
}
