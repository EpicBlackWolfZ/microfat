package conformance_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ratioBasisPointsDenominator = 10000.0
	arm64BaseRank               = 80
)

type conformanceVectors struct {
	FormatV2                []formatV2Case       `json:"format_v2"`
	MicroarchAMD64          []amd64Case          `json:"microarch_amd64"`
	MicroarchARM64          []arm64Case          `json:"microarch_arm64"`
	MicroarchSelection      []selectionCase      `json:"microarch_selection"`
	CgroupRetainedMemory    []retainedCase       `json:"cgroup_retained_memory"`
	CgroupExtractionMemory  []extractionCase     `json:"cgroup_extraction_memory"`
	CgroupEffectiveMemory   []effectiveCase      `json:"cgroup_effective_memory"`
	CgroupGOMEMLIMIT        []gomemlimitCase     `json:"cgroup_gomemlimit"`
	CgroupZstdDecoderMemory []zstdDecoderMemCase `json:"cgroup_zstd_decoder_memory"`
}

type formatV2Case struct {
	Name        string `json:"name"`
	IndexOffset int64  `json:"index_offset"`
	WireHex     string `json:"wire_hex"`
	ValidIndex  bool   `json:"valid_index"`
	BoundsValid bool   `json:"bounds_valid"`
}

type amd64Case struct {
	Name  string `json:"name"`
	Mask  uint32 `json:"mask"`
	Level string `json:"level"`
	Rank  int    `json:"rank"`
}

type arm64Case struct {
	Name          string `json:"name"`
	Mask          uint32 `json:"mask"`
	SupportedMask uint16 `json:"supported_mask"`
	HighestLevel  string `json:"highest_level"`
}

type selectionCase struct {
	Name             string   `json:"name"`
	Arch             string   `json:"arch"`
	X86Mask          uint32   `json:"x86_mask"`
	ARM64Mask        uint32   `json:"arm64_mask"`
	Available        []string `json:"available"`
	ForceLevel       *string  `json:"force_level"`
	MaxLevel         *string  `json:"max_level"`
	Disabled         []string `json:"disabled"`
	ExpectedSelected *string  `json:"expected_selected"`
}

type retainedCase struct {
	Limit    int64 `json:"limit"`
	Retained int64 `json:"retained"`
	Expected int64 `json:"expected"`
}

type extractionCase struct {
	Payload    int64  `json:"payload"`
	Dictionary int64  `json:"dictionary"`
	Decoder    int64  `json:"decoder"`
	Reserve    int64  `json:"reserve"`
	Expected   *int64 `json:"expected"`
}

type effectiveCase struct {
	MaxBytes  *int64 `json:"max_bytes"`
	HighBytes *int64 `json:"high_bytes"`
	Expected  *int64 `json:"expected"`
}

type gomemlimitCase struct {
	Limit       int64 `json:"limit"`
	RatioBps    int64 `json:"ratio_bps"`
	MinHeadroom int64 `json:"min_headroom"`
	Expected    int64 `json:"expected"`
}

type zstdDecoderMemCase struct {
	Window     int64  `json:"window"`
	MaxWindow  int64  `json:"max_window"`
	Overhead   int64  `json:"overhead"`
	MaxDecoder int64  `json:"max_decoder"`
	Expected   *int64 `json:"expected"`
}

func loadVectors(t *testing.T) conformanceVectors {
	t.Helper()
	path := filepath.Join("..", "..", "formal", "conformance", "vectors.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var v conformanceVectors
	require.NoError(t, json.Unmarshal(raw, &v))
	return v
}

func x86FromMask(m uint32) microarch.X86Features {
	bit := func(pos uint) bool { return (m & (uint32(1) << pos)) != 0 }
	return microarch.X86Features{
		HasLAHFSAHF: bit(0),
		HasCX16:     bit(1),
		HasPOPCNT:   bit(2),
		HasSSE3:     bit(3),
		HasSSSE3:    bit(4),
		HasSSE41:    bit(5),
		HasSSE42:    bit(6),
		HasAVX:      bit(7),
		HasAVX2:     bit(8),
		HasBMI1:     bit(9),
		HasBMI2:     bit(10),
		HasFMA:      bit(11),
		HasOSXSAVE:  bit(12),
		HasF16C:     bit(13),
		HasLZCNT:    bit(14),
		HasMOVBE:    bit(15),
		HasAVX512F:  bit(16),
		HasAVX512BW: bit(17),
		HasAVX512CD: bit(18),
		HasAVX512DQ: bit(19),
		HasAVX512VL: bit(20),
	}
}

func arm64FromMask(m uint32) microarch.ARM64Features {
	bit := func(pos uint) bool { return (m & (uint32(1) << pos)) != 0 }
	return microarch.ARM64Features{
		HasFP:      bit(0),
		HasASIMD:   bit(1),
		HasATOMICS: bit(2),
		HasCRC32:   bit(3),
		HasFPHP:    bit(4),
		HasASIMDHP: bit(5),
		HasJSCVT:   bit(6),
		HasFCMA:    bit(7),
		HasLRCPC:   bit(8),
		HasDCPOP:   bit(9),
		HasASIMDDP: bit(10),
		HasDIT:     bit(11),
		HasI8MM:    bit(12),
		HasBF16:    bit(13),
		HasWFxT:    bit(14),
		HasMOPS:    bit(15),
		HasNMI:     bit(16),
		HasHBC:     bit(17),
		HasGCS:     bit(18),
		HasTHE:     bit(19),
		HasSVE:     bit(20),
		HasSVE2:    bit(21),
		HasSME:     bit(22),
		HasSME2:    bit(23),
	}
}

func arm64PresentFeatureNames(f microarch.ARM64Features) []string {
	type flagEntry struct {
		name string
		on   bool
	}
	entries := []flagEntry{
		{"fp", f.HasFP}, {"asimd", f.HasASIMD}, {"atomics", f.HasATOMICS}, {"crc32", f.HasCRC32},
		{"fphp", f.HasFPHP}, {"asimdhp", f.HasASIMDHP}, {"jscvt", f.HasJSCVT}, {"fcma", f.HasFCMA},
		{"lrcpc", f.HasLRCPC}, {"dcpop", f.HasDCPOP}, {"asimddp", f.HasASIMDDP}, {"dit", f.HasDIT},
		{"i8mm", f.HasI8MM}, {"bf16", f.HasBF16}, {"wfxt", f.HasWFxT}, {"mops", f.HasMOPS},
		{"nmi", f.HasNMI}, {"hbc", f.HasHBC}, {"gcs", f.HasGCS}, {"the", f.HasTHE},
		{"sve", f.HasSVE}, {"sve2", f.HasSVE2}, {"sme", f.HasSME}, {"sme2", f.HasSME2},
	}
	var out []string
	for _, e := range entries {
		if e.on {
			out = append(out, e.name)
		}
	}
	return out
}

func TestLeanConformanceFormatV2(t *testing.T) {
	t.Parallel()
	vectors := loadVectors(t)
	require.NotEmpty(t, vectors.FormatV2)
	for _, tc := range vectors.FormatV2 {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			wire, err := hex.DecodeString(tc.WireHex)
			require.NoError(t, err)

			idx, err := format.UnmarshalBinaryIndex(wire)
			if !tc.ValidIndex {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			boundsErr := idx.ValidateBounds(tc.IndexOffset)
			if tc.BoundsValid {
				assert.NoError(t, boundsErr)
				remarshaled, marshalErr := format.MarshalBinaryIndex(idx)
				require.NoError(t, marshalErr)
				roundtrip, rtErr := format.UnmarshalBinaryIndex(remarshaled)
				require.NoError(t, rtErr)
				assert.Equal(t, idx, roundtrip)
			} else {
				assert.Error(t, boundsErr)
			}
		})
	}
}

func TestLeanConformanceMicroarch(t *testing.T) {
	t.Parallel()
	vectors := loadVectors(t)

	for _, tc := range vectors.MicroarchAMD64 {
		t.Run("amd64/"+tc.Name, func(t *testing.T) {
			t.Parallel()
			lvl := microarch.EvaluateAMD64(x86FromMask(tc.Mask))
			assert.Equal(t, tc.Level, lvl)
			assert.Equal(t, tc.Rank, microarch.Rank(microarch.ArchAMD64, lvl))
		})
	}

	for _, tc := range vectors.MicroarchARM64 {
		t.Run("arm64/"+tc.Name, func(t *testing.T) {
			t.Parallel()
			feat := arm64FromMask(tc.Mask)
			highest, statuses := microarch.EvaluateARM64Detailed(feat)
			assert.Equal(t, tc.HighestLevel, highest)
			assert.Equal(t, tc.HighestLevel, microarch.EvaluateARM64(feat))

			var gotMask uint16
			for _, st := range statuses {
				if st.Satisfied {
					r := microarch.Rank(microarch.ArchARM64, st.Level)
					require.GreaterOrEqual(t, r, arm64BaseRank)
					gotMask |= uint16(1) << uint(r-arm64BaseRank)
				}
			}
			assert.Equal(t, tc.SupportedMask, gotMask)
		})
	}

	for _, tc := range vectors.MicroarchSelection {
		t.Run("select/"+tc.Name, func(t *testing.T) {
			t.Parallel()
			var host microarch.Info
			if tc.Arch == microarch.ArchAMD64 {
				host = microarch.Info{
					Arch:  microarch.ArchAMD64,
					Level: microarch.EvaluateAMD64(x86FromMask(tc.X86Mask)),
				}
			} else {
				feat := arm64FromMask(tc.ARM64Mask)
				host = microarch.Info{
					Arch:     microarch.ArchARM64,
					Level:    microarch.EvaluateARM64(feat),
					Features: arm64PresentFeatureNames(feat),
				}
			}
			var pol microarch.Policy
			if tc.ForceLevel != nil {
				pol.ForceLevel = *tc.ForceLevel
			}
			if tc.MaxLevel != nil {
				pol.MaxLevel = *tc.MaxLevel
			}
			pol.DisabledVariants = tc.Disabled

			res, err := microarch.SelectVariantForHost(tc.Arch, host, tc.Available, pol)
			if tc.ExpectedSelected == nil {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, *tc.ExpectedSelected, res.SelectedVariant)
			}
		})
	}
}

func TestLeanConformanceCgroupMath(t *testing.T) {
	t.Parallel()
	vectors := loadVectors(t)

	for _, tc := range vectors.CgroupRetainedMemory {
		got := cgroup.RetainedMemoryLimit(tc.Limit, tc.Retained)
		assert.Equal(t, tc.Expected, got)
	}

	for _, tc := range vectors.CgroupExtractionMemory {
		got, err := cgroup.ExtractionMemory(tc.Payload, tc.Dictionary, tc.Decoder, tc.Reserve)
		if tc.Expected == nil {
			assert.Error(t, err)
		} else {
			require.NoError(t, err)
			assert.Equal(t, *tc.Expected, got)
		}
	}

	for _, tc := range vectors.CgroupEffectiveMemory {
		var maxB, highB int64
		if tc.MaxBytes != nil {
			maxB = *tc.MaxBytes
		}
		if tc.HighBytes != nil {
			highB = *tc.HighBytes
		}
		got := cgroup.CalculateEffectiveMemoryLimit(maxB, highB)
		if tc.Expected == nil {
			assert.Zero(t, got)
		} else {
			assert.Equal(t, *tc.Expected, got)
		}
	}

	for _, tc := range vectors.CgroupGOMEMLIMIT {
		ratio := float64(tc.RatioBps) / ratioBasisPointsDenominator
		got, ok := cgroup.CalculateGOMEMLIMIT(tc.Limit, ratio, tc.MinHeadroom)
		require.True(t, ok)
		assert.Equal(t, tc.Expected, got)
	}

	for _, tc := range vectors.CgroupZstdDecoderMemory {
		got, err := cgroup.ZstdDecoderMemory(tc.Window, tc.MaxWindow, tc.Overhead, tc.MaxDecoder)
		if tc.Expected == nil {
			assert.Error(t, err)
		} else {
			require.NoError(t, err)
			assert.Equal(t, *tc.Expected, got)
		}
	}
}
