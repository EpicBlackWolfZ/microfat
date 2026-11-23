package compress

import (
	"bytes"
	"debug/elf"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	testChunkSize64K  = 64 * 1024
	testDecodeWorkers = 4
)

func mutateVariantTextSection(t *testing.T, baseELF []byte, xorByte byte) []byte {
	t.Helper()
	out := make([]byte, len(baseELF))
	copy(out, baseELF)
	ranges, _, err := ExecutableRangesELF(out)
	require.NoError(t, err)
	require.NotEmpty(t, ranges)
	// Modify a tiny call immediate inside .text so .rodata and most .text still share identical blocks.
	mid := (ranges[0].Start + ranges[0].End) / 2
	out[mid] ^= xorByte
	return out
}

func TestV3PackDecodeMemfdRoundTripAndDedup(t *testing.T) {
	t.Parallel()

	exePath, err := os.Executable()
	require.NoError(t, err)
	v1Raw, err := os.ReadFile(exePath) // #nosec G304 -- reading current test executable
	require.NoError(t, err)

	v2Raw := mutateVariantTextSection(t, v1Raw, 0x01)
	v3Raw := mutateVariantTextSection(t, v1Raw, 0x02)

	for _, mode := range []ChunkMode{ChunkFixed, ChunkSectionCDC} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			_, stats, packErr := PackV3(&buf, V3PackOptions{
				TargetArch:     "amd64",
				ChunkMode:      mode,
				AvgChunkSize:   testChunkSize64K,
				EnableBCJRIP:   true,
				ParallelEncode: testDecodeWorkers,
				Variants: map[string][]byte{
					"v1": v1Raw,
					"v2": v2Raw,
					"v3": v3Raw,
				},
			})
			require.NoError(t, packErr)
			require.Greater(t, stats.DeduplicatedBytes, int64(0), "expected substantial cross-variant block dedup")

			rawArchive := buf.Bytes()
			reader := bytes.NewReader(rawArchive)
			arch, parseErr := ReadV3Archive(reader, int64(len(rawArchive)))
			require.NoError(t, parseErr)
			require.Equal(t, uint16(elf.EM_X86_64), arch.Machine)

			for lvl, wantBytes := range map[string][]byte{"v1": v1Raw, "v2": v2Raw, "v3": v3Raw} {
				gotBuf, decErr := DecodeVariantBytes(arch, reader, lvl, testDecodeWorkers)
				require.NoError(t, decErr)
				require.Equal(t, wantBytes, gotBuf)

				fd, memErr := DecodeVariantToSealedMemfd(arch, reader, lvl, testDecodeWorkers, true)
				require.NoError(t, memErr)
				memBytes := make([]byte, len(wantBytes))
				require.NoError(t, preadAll(fd, memBytes, 0))
				require.Equal(t, wantBytes, memBytes)
				_ = unix.Close(fd)
			}
		})
	}
}

func TestV3RejectsTamperedContainer(t *testing.T) {
	t.Parallel()

	exePath, err := os.Executable()
	require.NoError(t, err)
	raw, err := os.ReadFile(exePath) // #nosec G304 -- test executable
	require.NoError(t, err)

	var buf bytes.Buffer
	_, _, err = PackV3(&buf, V3PackOptions{
		TargetArch:   "amd64",
		ChunkMode:    ChunkFixed,
		AvgChunkSize: testChunkSize64K,
		EnableBCJRIP: false,
		Variants:     map[string][]byte{"v1": raw},
	})
	require.NoError(t, err)

	corrupted := append([]byte(nil), buf.Bytes()...)
	corrupted[v3HeaderLen+1] ^= 0xFF
	r := bytes.NewReader(corrupted)
	arch, err := ReadV3Archive(r, int64(len(corrupted)))
	require.NoError(t, err)

	_, err = DecodeVariantBytes(arch, r, "v1", 2)
	require.Error(t, err)

	corrupted[len(corrupted)-v3TrailerSize-1] ^= 0xFF
	_, err = ReadV3Archive(bytes.NewReader(corrupted), int64(len(corrupted)))
	require.Error(t, err)
}
