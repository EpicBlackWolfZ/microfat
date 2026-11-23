package compress

import (
	"debug/elf"
	"math/rand"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	leanARM64BasePos = uint32(0x1000)
	fuzzIterations   = 200
	fuzzMaxLen       = 512
	fuzzSeed         = int64(20261123)
	byteSpace        = 256
)

func TestLeanConformanceX86_64(t *testing.T) {
	t.Parallel()

	input := []byte{
		0x55,
		0xE8, 0x10, 0x00, 0x00, 0x00,
		0x48, 0x8D, 0x05, 0x20, 0x01, 0x00, 0x00,
		0x0F, 0x84, 0x08, 0x00, 0x00, 0x00,
		0x48, 0x8D, 0x00, 0xE8,
	}
	// Exact output verified from `lake env lean` #eval on Microfat.ElfFilter.x86Transform.
	wantFiltered := []byte{
		85, 232, 22, 0, 0, 0,
		72, 141, 5, 45, 1, 0, 0,
		15, 132, 27, 0, 0, 0,
		72, 141, 0, 232,
	}

	gotFiltered := FilterX86_64(input, 0)
	require.Equal(t, wantFiltered, gotFiltered)
	gotRoundTrip := UnfilterX86_64(gotFiltered, 0)
	require.Equal(t, input, gotRoundTrip)
}

func TestLeanConformanceARM64(t *testing.T) {
	t.Parallel()

	input := []byte{
		0x01, 0x00, 0x00, 0x94,
		0xFE, 0xFF, 0xFF, 0x17,
		0x00, 0x00, 0x00, 0x90,
		0x1F, 0x20, 0x03, 0xD5,
		0xAA, 0xBB,
	}
	// Exact output verified from `lake env lean` #eval on Microfat.ElfFilter.arm64Transform.
	wantFiltered := []byte{
		1, 4, 0, 148,
		255, 3, 0, 20,
		0, 0, 0, 176,
		31, 32, 3, 213,
		170, 187,
	}

	gotFiltered := FilterARM64(input, leanARM64BasePos)
	require.Equal(t, wantFiltered, gotFiltered)
	gotRoundTrip := UnfilterARM64(gotFiltered, leanARM64BasePos)
	require.Equal(t, input, gotRoundTrip)
}

func TestAdversarialX86_64RoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		data []byte
		pos  uint32
	}{
		{name: "empty", data: nil, pos: 0},
		{name: "truncated_e8", data: []byte{0xE8, 0x01, 0x02, 0x03}, pos: 0},
		{name: "truncated_0f80", data: []byte{0x0F, 0x80, 0x01, 0x02, 0x03}, pos: 7},
		{name: "double_rex_rip", data: []byte{0x48, 0x48, 0x8D, 0x05, 0xFF, 0xFF, 0xFF, 0xFF}, pos: 0xFFFFFFFC},
		{name: "rex_non_rip_modrm", data: []byte{0x48, 0x8D, 0x00, 0xE8, 0x01, 0x02, 0x03, 0x04}, pos: 13},
		{name: "false_prefix_0f_e8", data: []byte{0x0F, 0xE8, 0x01, 0x02, 0x03, 0x04}, pos: 100},
		{name: "rex_then_ripOp_non_modrm", data: []byte{0x4C, 0x8B, 0x06, 0x0F, 0x85, 0x10, 0x20, 0x30, 0x40}, pos: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			enc := FilterX86_64(tc.data, tc.pos)
			require.Len(t, enc, len(tc.data))
			dec := UnfilterX86_64(enc, tc.pos)
			require.Equal(t, tc.data, dec)
			// Verify opposite direction invertibility too.
			inv := FilterX86_64(UnfilterX86_64(tc.data, tc.pos), tc.pos)
			require.Equal(t, tc.data, inv)
		})
	}
}

func TestRandomAndWrapRoundTrips(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(fuzzSeed)) // #nosec G404 -- deterministic test RNG
	opcodes := []byte{0xE8, 0xE9, 0x0F, 0x80, 0x84, 0x48, 0x4C, 0x8D, 0x8B, 0x89, 0x05, 0x00, 0xFF}

	for iter := range fuzzIterations {
		n := rng.Intn(fuzzMaxLen)
		buf := make([]byte, n)
		for i := range buf {
			if rng.Intn(arm64InsnLen) == 0 {
				buf[i] = opcodes[rng.Intn(len(opcodes))]
			} else {
				buf[i] = byte(rng.Intn(byteSpace))
			}
		}
		base := uint32(0xFFFFFFE0) + uint32(iter) // #nosec G115 -- deliberate wrap test
		require.Equal(t, buf, UnfilterX86_64(FilterX86_64(buf, base), base))
		require.Equal(t, buf, UnfilterARM64(FilterARM64(buf, base), base))
	}
}

func TestCurrentExecutableELFFilterRoundTrip(t *testing.T) {
	t.Parallel()

	exePath, err := os.Executable()
	require.NoError(t, err)
	raw, err := os.ReadFile(exePath) // #nosec G304 -- reading current test executable path
	require.NoError(t, err)

	ranges, machine, err := ExecutableRangesELF(raw)
	require.NoError(t, err)
	require.NotEmpty(t, ranges)
	require.Equal(t, elf.EM_X86_64, machine)

	buf := make([]byte, len(raw))
	copy(buf, raw)
	require.NoError(t, ApplyELFFilter(buf, ranges, machine, true))
	require.NotEqual(t, raw, buf, "x86-64 BCJ+RIP filter should modify real Go test binary .text")
	require.NoError(t, ApplyELFFilter(buf, ranges, machine, false))
	require.Equal(t, raw, buf)
}

func TestSnapX86FilterCutsAndChunkLocalEquivalence(t *testing.T) {
	t.Parallel()

	// Layout: NOP(0), CALL rel32(1..6), RIP LEA(6..13), Jcc rel32(13..19), NOP(19), REX non-RIP(20..22), JMP rel32(22..27).
	section := []byte{
		0x90,
		0xE8, 0x10, 0x00, 0x00, 0x00,
		0x48, 0x8D, 0x05, 0x20, 0x01, 0x00, 0x00,
		0x0F, 0x84, 0x08, 0x00, 0x00, 0x00,
		0x90,
		0x48, 0x8D,
		0xE9, 0xAA, 0xBB, 0xCC, 0xDD,
	}
	// Candidate cuts hitting inside CALL(3), inside CALL(5), inside LEA(9), inside Jcc(16), at boundary(19), and OOB(99).
	snapped := SnapX86FilterCuts(section, []uint64{0, 3, 5, 9, 16, 19, 99})
	require.Equal(t, []uint64{6, 13, 19}, snapped)

	mono := FilterX86_64(section, 0)
	bounds := append(append([]uint64{0}, snapped...), uint64(len(section)))
	rebuilt := make([]byte, len(section))
	for i := 0; i+1 < len(bounds); i++ {
		s, e := bounds[i], bounds[i+1]
		chunk := append([]byte(nil), section[s:e]...)
		require.NoError(t, ApplyExtentFilterInPlace(chunk, elf.EM_X86_64, uint32(s), true)) // #nosec G115 -- test offset
		copy(rebuilt[s:e], chunk)
	}
	require.Equal(t, mono, rebuilt, "snapped chunk-local filtering must match monolithic section filtering")

	unfiltered := make([]byte, len(section))
	for i := 0; i+1 < len(bounds); i++ {
		s, e := bounds[i], bounds[i+1]
		chunk := append([]byte(nil), rebuilt[s:e]...)
		require.NoError(t, ApplyExtentFilterInPlace(chunk, elf.EM_X86_64, uint32(s), false)) // #nosec G115 -- test offset
		copy(unfiltered[s:e], chunk)
	}
	require.Equal(t, section, unfiltered)
}

func TestARM64ChunkLocalAlignedEquivalence(t *testing.T) {
	t.Parallel()

	section := []byte{
		0x01, 0x00, 0x00, 0x94,
		0xFE, 0xFF, 0xFF, 0x17,
		0x00, 0x00, 0x00, 0x90,
		0x1F, 0x20, 0x03, 0xD5,
		0xAA, 0xBB,
	}
	const split = uint64(8)
	mono := FilterARM64(section, 0)

	partA := append([]byte(nil), section[:split]...)
	partB := append([]byte(nil), section[split:]...)
	require.NoError(t, ApplyExtentFilterInPlace(partA, elf.EM_AARCH64, 0, true))
	require.NoError(t, ApplyExtentFilterInPlace(partB, elf.EM_AARCH64, uint32(split), true)) // #nosec G115 -- small test split
	require.Equal(t, mono, append(partA, partB...))

	require.NoError(t, ApplyExtentFilterInPlace(partA, elf.EM_AARCH64, 0, false))
	require.NoError(t, ApplyExtentFilterInPlace(partB, elf.EM_AARCH64, uint32(split), false)) // #nosec G115 -- small test split
	require.Equal(t, section, append(partA, partB...))
}
