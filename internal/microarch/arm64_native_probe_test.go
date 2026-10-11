package microarch

import (
	"encoding/binary"
	"runtime"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/cpu"
)

// These fixtures encode Linux arm64 UAPI bits numerically so a wrong production
// constant cannot silently change both the probe and its expected input.
const (
	fixtureATHWCAP  = uint64(16)
	fixtureATHWCAP2 = uint64(26)
	fixtureGCS      = uint64(0x1_0000_0000)
	fixtureBF16     = uint64(0x4000)
	fixtureWFxT     = uint64(0x8000_0000)
	fixtureSME      = uint64(0x80_0000)
	fixtureSME2     = uint64(0x20_0000_0000)
	fixtureMOPS     = uint64(0x800_0000_0000)
	fixtureHBC      = uint64(0x1000_0000_0000)
	fixtureBF16WFxT = fixtureBF16 | fixtureWFxT
	fixtureAllHigh  = fixtureMOPS | fixtureHBC
	fixtureEntryLen = 16
	fixtureWordLen  = 8
	fixtureNMIName  = "nmi"
	fixtureTHEName  = "the"
)

type arm64AuxvEntry struct {
	tag   uint64
	value uint64
}

func encodeARM64Auxv(entries ...arm64AuxvEntry) []byte {
	data := make([]byte, fixtureEntryLen*len(entries))
	for i, entry := range entries {
		offset := fixtureEntryLen * i
		binary.LittleEndian.PutUint64(data[offset:offset+fixtureWordLen], entry.tag)
		binary.LittleEndian.PutUint64(data[offset+fixtureWordLen:offset+fixtureEntryLen], entry.value)
	}
	return data
}

func TestParseLinuxAuxvARM64NativeFlags(t *testing.T) {
	t.Parallel()

	allFlags := ARM64Features{
		HasBF16: true, HasWFxT: true, HasSME: true, HasSME2: true,
		HasMOPS: true, HasHBC: true, HasGCS: true,
	}
	allHWCAP2 := fixtureBF16WFxT | fixtureSME | fixtureSME2 | fixtureAllHigh
	tests := []struct {
		name string
		data []byte
		want ARM64Features
	}{
		{
			name: "empty input",
		},
		{
			name: "truncated first entry",
			data: encodeARM64Auxv(arm64AuxvEntry{fixtureATHWCAP, fixtureGCS})[:fixtureEntryLen-1],
		},
		{
			name: "all known auxiliary vector flags",
			data: encodeARM64Auxv(
				arm64AuxvEntry{fixtureATHWCAP, fixtureGCS},
				arm64AuxvEntry{fixtureATHWCAP2, allHWCAP2},
			),
			want: allFlags,
		},
		{
			name: "HWCAP2 preceding HWCAP",
			data: encodeARM64Auxv(
				arm64AuxvEntry{fixtureATHWCAP2, allHWCAP2},
				arm64AuxvEntry{fixtureATHWCAP, fixtureGCS},
			),
			want: allFlags,
		},
		{
			name: "GCS alone above bit 31",
			data: encodeARM64Auxv(arm64AuxvEntry{fixtureATHWCAP, fixtureGCS}),
			want: ARM64Features{HasGCS: true},
		},
		{
			name: "MOPS alone above bit 31",
			data: encodeARM64Auxv(arm64AuxvEntry{fixtureATHWCAP2, fixtureMOPS}),
			want: ARM64Features{HasMOPS: true},
		},
		{
			name: "HBC alone above bit 31",
			data: encodeARM64Auxv(arm64AuxvEntry{fixtureATHWCAP2, fixtureHBC}),
			want: ARM64Features{HasHBC: true},
		},
		{
			name: "BF16 alone",
			data: encodeARM64Auxv(arm64AuxvEntry{fixtureATHWCAP2, fixtureBF16}),
			want: ARM64Features{HasBF16: true},
		},
		{
			name: "WFxT alone",
			data: encodeARM64Auxv(arm64AuxvEntry{fixtureATHWCAP2, fixtureWFxT}),
			want: ARM64Features{HasWFxT: true},
		},
		{
			name: "SME alone",
			data: encodeARM64Auxv(arm64AuxvEntry{fixtureATHWCAP2, fixtureSME}),
			want: ARM64Features{HasSME: true},
		},
		{
			name: "SME2 alone above bit 31",
			data: encodeARM64Auxv(arm64AuxvEntry{fixtureATHWCAP2, fixtureSME2}),
			want: ARM64Features{HasSME2: true},
		},
		{
			name: "GCS bit in HWCAP2 is unrelated",
			data: encodeARM64Auxv(arm64AuxvEntry{fixtureATHWCAP2, fixtureGCS}),
		},
		{
			name: "HWCAP2 bits in HWCAP are unrelated",
			data: encodeARM64Auxv(arm64AuxvEntry{fixtureATHWCAP, allHWCAP2}),
		},
		{
			name: "adjacent HWCAP bits do not imply GCS",
			data: encodeARM64Auxv(arm64AuxvEntry{fixtureATHWCAP, 0x2_8000_0000}),
		},
		{
			name: "adjacent HWCAP2 bits do not imply MOPS or HBC",
			data: encodeARM64Auxv(arm64AuxvEntry{fixtureATHWCAP2, 0x2400_0000_0000}),
		},
		{
			name: "unknown tag cannot advertise features",
			data: encodeARM64Auxv(arm64AuxvEntry{1, fixtureGCS | allHWCAP2}),
		},
		{
			name: "AT_NULL terminates before later feature entries",
			data: encodeARM64Auxv(
				arm64AuxvEntry{},
				arm64AuxvEntry{fixtureATHWCAP, fixtureGCS},
				arm64AuxvEntry{fixtureATHWCAP2, allHWCAP2},
			),
		},
		{
			name: "AT_NULL preserves earlier features",
			data: encodeARM64Auxv(
				arm64AuxvEntry{fixtureATHWCAP2, fixtureMOPS},
				arm64AuxvEntry{},
				arm64AuxvEntry{fixtureATHWCAP, fixtureGCS},
			),
			want: ARM64Features{HasMOPS: true},
		},
		{
			name: "truncated final entry does not affect complete entries",
			data: encodeARM64Auxv(
				arm64AuxvEntry{fixtureATHWCAP, fixtureGCS},
				arm64AuxvEntry{fixtureATHWCAP2, allHWCAP2},
			)[:fixtureEntryLen+fixtureWordLen],
			want: ARM64Features{HasGCS: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, parseLinuxAuxvARM64(tt.data))
		})
	}
}

func TestCurrentARM64FeaturesNativeAuxvRegression(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native auxiliary vector collection is Linux-specific")
	}
	previousReader := readLinuxAuxvARM64Func
	previousCPU := cpu.ARM64
	t.Cleanup(func() {
		readLinuxAuxvARM64Func = previousReader
		cpu.ARM64 = previousCPU
	})

	cpu.ARM64.HasFP = true
	cpu.ARM64.HasASIMD = true
	cpu.ARM64.HasATOMICS = true
	cpu.ARM64.HasCRC32 = true
	cpu.ARM64.HasFPHP = true
	cpu.ARM64.HasASIMDHP = true
	cpu.ARM64.HasJSCVT = true
	cpu.ARM64.HasFCMA = true
	cpu.ARM64.HasLRCPC = true
	cpu.ARM64.HasDCPOP = true
	cpu.ARM64.HasASIMDDP = true
	cpu.ARM64.HasDIT = true
	cpu.ARM64.HasI8MM = true
	cpu.ARM64.HasSVE = false
	cpu.ARM64.HasSVE2 = false

	tests := []struct {
		name          string
		hwcap         uint64
		hwcap2        uint64
		sve           bool
		sve2          bool
		wantLevel     string
		wantMissing88 []string
		wantMissing89 []string
	}{
		{
			name:  "available MOPS HBC GCS retain conservative v8.7",
			hwcap: fixtureGCS, hwcap2: fixtureBF16WFxT | fixtureAllHigh,
			wantLevel: ARM64v8_7, wantMissing88: []string{fixtureNMIName}, wantMissing89: []string{fixtureTHEName},
		},
		{
			name:  "missing MOPS stays unadvertised",
			hwcap: fixtureGCS, hwcap2: fixtureBF16WFxT | fixtureHBC,
			wantLevel: ARM64v8_7, wantMissing88: []string{"mops", fixtureNMIName}, wantMissing89: []string{fixtureTHEName},
		},
		{
			name:  "missing HBC stays unadvertised",
			hwcap: fixtureGCS, hwcap2: fixtureBF16WFxT | fixtureMOPS,
			wantLevel: ARM64v8_7, wantMissing88: []string{fixtureNMIName, "hbc"}, wantMissing89: []string{fixtureTHEName},
		},
		{
			name:      "missing GCS stays unadvertised",
			hwcap2:    fixtureBF16WFxT | fixtureAllHigh,
			wantLevel: ARM64v8_7, wantMissing88: []string{fixtureNMIName}, wantMissing89: []string{"gcs", fixtureTHEName},
		},
		{
			name:  "independent SVE branch reaches v9.1",
			hwcap: fixtureGCS, hwcap2: fixtureBF16WFxT | fixtureAllHigh, sve: true,
			wantLevel: ARM64v9_1, wantMissing88: []string{fixtureNMIName}, wantMissing89: []string{fixtureTHEName},
		},
		{
			name:  "independent SVE2 branch reaches v9.2",
			hwcap: fixtureGCS, hwcap2: fixtureBF16WFxT | fixtureAllHigh, sve: true, sve2: true,
			wantLevel: ARM64v9_2, wantMissing88: []string{fixtureNMIName}, wantMissing89: []string{fixtureTHEName},
		},
		{
			name:  "independent SME branch reaches v9.4",
			hwcap: fixtureGCS, hwcap2: fixtureBF16WFxT | fixtureAllHigh | fixtureSME, sve: true, sve2: true,
			wantLevel: ARM64v9_4, wantMissing88: []string{fixtureNMIName}, wantMissing89: []string{fixtureTHEName},
		},
		{
			name:  "independent SME2 branch reaches v9.5",
			hwcap: fixtureGCS, hwcap2: fixtureBF16WFxT | fixtureAllHigh | fixtureSME | fixtureSME2, sve: true, sve2: true,
			wantLevel: ARM64v9_5, wantMissing88: []string{fixtureNMIName}, wantMissing89: []string{fixtureTHEName},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu.ARM64.HasSVE = tt.sve
			cpu.ARM64.HasSVE2 = tt.sve2
			data := encodeARM64Auxv(
				arm64AuxvEntry{fixtureATHWCAP, tt.hwcap},
				arm64AuxvEntry{fixtureATHWCAP2, tt.hwcap2},
			)
			readLinuxAuxvARM64Func = func() ARM64Features {
				return parseLinuxAuxvARM64(data)
			}

			features := currentARM64Features()
			assert.Equal(t, tt.hwcap2&fixtureMOPS != 0, features.HasMOPS)
			assert.Equal(t, tt.hwcap2&fixtureHBC != 0, features.HasHBC)
			assert.Equal(t, tt.hwcap&fixtureGCS != 0, features.HasGCS)
			assert.False(t, features.HasNMI, "Linux auxv has no authoritative NMI flag")
			assert.False(t, features.HasTHE, "Linux auxv has no authoritative THE flag")

			info := detectForArch("linux", ArchARM64, X86Features{}, features)
			require.Equal(t, tt.wantLevel, info.Level)
			assert.Equal(t, features.HasMOPS, slices.Contains(info.Features, "mops"))
			assert.Equal(t, features.HasHBC, slices.Contains(info.Features, "hbc"))
			assert.Equal(t, features.HasGCS, slices.Contains(info.Features, "gcs"))
			assert.NotContains(t, info.Features, fixtureNMIName)
			assert.NotContains(t, info.Features, fixtureTHEName)

			statuses := make(map[string]ARM64LevelStatus, len(info.Levels))
			for _, status := range info.Levels {
				statuses[status.Level] = status
			}
			require.Contains(t, statuses, ARM64v8_7)
			require.Contains(t, statuses, ARM64v8_8)
			require.Contains(t, statuses, ARM64v8_9)
			assert.True(t, statuses[ARM64v8_7].Satisfied)
			assert.False(t, statuses[ARM64v8_8].Satisfied)
			assert.False(t, statuses[ARM64v8_9].Satisfied)
			assert.ElementsMatch(t, tt.wantMissing88, statuses[ARM64v8_8].MissingFeatures)
			assert.ElementsMatch(t, tt.wantMissing89, statuses[ARM64v8_9].MissingFeatures)
			assert.Empty(t, statuses[ARM64v8_8].MissingPrereqs)
			assert.Equal(t, []string{ARM64v8_8}, statuses[ARM64v8_9].MissingPrereqs)

			available := []string{ARM64v8_9, ARM64v8_8, ARM64v8_7, ARM64v9_1, ARM64v9_2, ARM64v9_4, ARM64v9_5}
			selected, err := SelectVariantForHost(ArchARM64, info, available, Policy{})
			require.NoError(t, err)
			assert.Equal(t, tt.wantLevel, selected.SelectedVariant)
			_, err = SelectVariantForHost(ArchARM64, info, []string{ARM64v8_8, ARM64v8_9}, Policy{})
			require.ErrorIs(t, err, ErrNoMatchingVariant)
			for _, forceLevel := range []string{ARM64v8_8, ARM64v8_9} {
				_, err = SelectVariantForHost(ArchARM64, info, available, Policy{ForceLevel: forceLevel})
				require.ErrorIs(t, err, ErrIncompatibleForcedVariant)
			}
		})
	}
}
