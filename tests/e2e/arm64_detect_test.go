package e2e_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	nativeARM64AuxvWordBytes  = 8
	nativeARM64AuxvEntryBytes = 2 * nativeARM64AuxvWordBytes
	nativeARM64ATHWCAP        = 16
	nativeARM64ATHWCAP2       = 26
	nativeARM64GCSBit         = 32
	nativeARM64MOPSBit        = 43
	nativeARM64HBCBit         = 44
)

type nativeARM64DetectLevel struct {
	Level           string   `json:"level"`
	Satisfied       bool     `json:"satisfied"`
	MissingFeatures []string `json:"missing_features"`
}

type nativeARM64DetectInfo struct {
	OS       string                   `json:"os"`
	Arch     string                   `json:"arch"`
	Level    string                   `json:"level"`
	Features []string                 `json:"features"`
	Levels   []nativeARM64DetectLevel `json:"levels"`
}

// Read raw Linux ABI values independently of the microarch collector so this
// comparison can expose an incorrect HWCAP word or bit assignment.
func nativeARM64Auxv(t *testing.T) (uint64, uint64) {
	t.Helper()
	data, err := os.ReadFile("/proc/self/auxv")
	require.NoError(t, err)
	require.Zero(t, len(data)%nativeARM64AuxvEntryBytes, "auxv must contain complete 64-bit entries")

	var hwcap, hwcap2 uint64
	var foundHWCAP, foundHWCAP2, terminated bool
	for offset := 0; offset < len(data); offset += nativeARM64AuxvEntryBytes {
		entry := data[offset : offset+nativeARM64AuxvEntryBytes]
		key := binary.LittleEndian.Uint64(entry[:nativeARM64AuxvWordBytes])
		value := binary.LittleEndian.Uint64(entry[nativeARM64AuxvWordBytes:])
		if key == 0 {
			terminated = true
			break
		}
		switch key {
		case nativeARM64ATHWCAP:
			hwcap, foundHWCAP = value, true
		case nativeARM64ATHWCAP2:
			hwcap2, foundHWCAP2 = value, true
		}
	}
	require.True(t, terminated, "kernel auxv must end with AT_NULL")
	require.True(t, foundHWCAP, "kernel auxv must report AT_HWCAP")
	require.True(t, foundHWCAP2, "kernel auxv must report AT_HWCAP2")
	return hwcap, hwcap2
}

func TestNativeARM64HighFeatureDetection(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" || runtime.GOARCH != archARM64 {
		t.Skip("native Linux/arm64 hardware required; feature collection is covered by auxv fixtures on other hosts")
	}

	hwcap, hwcap2 := nativeARM64Auxv(t)
	const timeout = 15 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, cliPath, "detect", "--json").CombinedOutput()
	require.NoError(t, ctx.Err(), "native ARM64 detection timed out: %s", output)
	require.NoError(t, err, string(output))
	var detected nativeARM64DetectInfo
	require.NoError(t, json.Unmarshal(output, &detected))
	require.Equal(t, "linux", detected.OS)
	require.Equal(t, archARM64, detected.Arch)
	require.NotEmpty(t, detected.Level)

	flags := []struct {
		name string
		word uint64
		bit  uint
	}{
		{name: "gcs", word: hwcap, bit: nativeARM64GCSBit},
		{name: "mops", word: hwcap2, bit: nativeARM64MOPSBit},
		{name: "hbc", word: hwcap2, bit: nativeARM64HBCBit},
	}
	var present []string
	for _, flag := range flags {
		observed := flag.word&(uint64(1)<<flag.bit) != 0
		if observed {
			present = append(present, flag.name)
		}
		t.Run(flag.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, observed, slices.Contains(detected.Features, flag.name),
				"detected feature must match the raw kernel auxv flag")
		})
	}
	t.Logf("architecture=%s selected_level=%s HWCAP=%#x HWCAP2=%#x observed_high_features=%v",
		detected.Arch, detected.Level, hwcap, hwcap2, present)
	if len(present) == 0 {
		t.Log("MOPS/HBC/GCS are absent; positive high-feature hardware exercise remains unqualified")
	}

	assert.NotContains(t, detected.Features, "nmi", "NMI has no Linux userspace feature signal")
	assert.NotContains(t, detected.Features, "the", "THE has no Linux userspace feature signal")
	for _, level := range []struct {
		name    string
		missing string
	}{
		{name: "v8.8", missing: "nmi"},
		{name: "v8.9", missing: "the"},
	} {
		t.Run(level.name, func(t *testing.T) {
			t.Parallel()
			index := slices.IndexFunc(detected.Levels, func(status nativeARM64DetectLevel) bool {
				return status.Level == level.name
			})
			require.NotEqual(t, -1, index, "detect must report a status for %s", level.name)
			assert.False(t, detected.Levels[index].Satisfied)
			assert.Contains(t, detected.Levels[index].MissingFeatures, level.missing)
		})
	}
}
