package pack

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	platformEdgePointerSize = 8
	platformEdgeWordCount   = 2
	platformEdgeInfoLines   = 3
	platformEdgeHeaderAddr  = platformInfoOffset + platformInfoHeader
	platformEdgeModuleAddr  = platformEdgeHeaderAddr + platformEdgePointerSize*platformEdgeWordCount
	platformEdgeModuleInfo  = "path\tapp\nbuild\tGOOS=freebsd\n"
	platformEdgeOSFreeBSD   = "freebsd"
)

// Incomplete or misplaced linker data is unavailable platform evidence. These
// executable fixtures ensure arbitrary application bytes do not become a GOOS
// assertion merely because they contain the linker magic or a build line.
func TestGoELFPlatformIncompleteInlineEvidence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		patch func([]byte) []byte
	}{
		{name: "empty toolchain version", patch: func(data []byte) []byte {
			data[platformInfoOffset+platformInfoHeader] = 0
			return data
		}},
		{name: "unterminated version length", patch: func(data []byte) []byte {
			for i := platformInfoOffset + platformInfoHeader; i < len(data); i++ {
				data[i] = 0x80
			}
			return data
		}},
		{name: "truncated metadata header", patch: func(data []byte) []byte {
			return data[:platformInfoOffset+len(platformInfoMagic)]
		}},
		{name: "metadata outside virtual alignment", patch: func(data []byte) []byte {
			data = append(data, 0)
			copy(data[platformInfoOffset+1:], data[platformInfoOffset:])
			data[platformInfoOffset] = 0
			return data
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := tc.patch(inlinePlatformELF(t, platformEdgeModuleInfo))
			updatePlatformEdgeLoadSize(data)
			err := ValidateELFBinary(writePlatformELF(t, data), testOSLinux, testArchAMD64)
			require.NoError(t, err)
		})
	}
}

func TestGoELFPlatformMalformedModuleEvidence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		module string
	}{
		{name: "incomplete final build line", module: strings.TrimSuffix(platformEdgeModuleInfo, "\n")},
		{name: "unparseable build setting after GOOS", module: platformEdgeModuleInfo + "build\tBROKEN\n"},
		{name: "malformed main module columns", module: "mod\tapp\n" + platformEdgeModuleInfo},
		{name: "malformed replacement columns", module: "mod\tapp\t(devel)\n=>\treplacement\n" + platformEdgeModuleInfo},
		{name: "valid replacement with Linux target", module: "mod\tapp\t(devel)\n=>\treplacement\tv1.0.0\tsum\n" +
			"build\tGOOS=linux\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := inlinePlatformELF(t, tc.module)
			err := ValidateELFBinary(writePlatformELF(t, data), testOSLinux, testArchAMD64)
			require.NoError(t, err)
		})
	}
}

// Legacy linker metadata points to Go string headers, whose addresses and
// lengths must be backed by a unique loaded file range before being inspected.
func TestLegacyGoELFPlatformUnavailablePointers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		offset int
		value  func([]byte) uint64
	}{
		{name: "unmapped version header", offset: platformInfoOffset + platformInfoAlign},
		{name: "unmapped module header", offset: platformInfoOffset + platformInfoAlign + platformEdgePointerSize},
		{name: "unmapped version bytes", offset: platformEdgeHeaderAddr},
		{name: "unmapped module bytes", offset: platformEdgeModuleAddr},
		{name: "empty version", offset: platformEdgeHeaderAddr + platformEdgePointerSize},
		{name: "empty module", offset: platformEdgeModuleAddr + platformEdgePointerSize},
		{name: "truncated string header", offset: platformInfoOffset + platformInfoAlign,
			value: func(data []byte) uint64 { return uint64(platformBaseAddress + len(data) - platformEdgePointerSize) }},
		{name: "string range extends beyond file data", offset: platformEdgeModuleAddr,
			value: func(data []byte) uint64 { return uint64(platformBaseAddress + len(data) - 1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := legacyPlatformELF(t, platformEdgePointerSize, binary.LittleEndian, platformEdgeOSFreeBSD)
			var value uint64
			if tc.value != nil {
				value = tc.value(data)
			}
			binary.LittleEndian.PutUint64(data[tc.offset:], value)
			err := ValidateELFBinary(writePlatformELF(t, data), testOSLinux, testArchAMD64)
			require.NoError(t, err)
		})
	}
}

func TestGoELFPlatformStringBudgets(t *testing.T) {
	t.Parallel()
	t.Run("combined inline version and module budget", func(t *testing.T) {
		t.Parallel()
		original := inlinePlatformELF(t, platformEdgeModuleInfo)
		versionLength, prefix := binary.Uvarint(original[platformInfoOffset+platformInfoHeader:])
		moduleOffset := uint64(platformInfoOffset+platformInfoHeader+prefix) + versionLength
		data := bytes.Clone(original[:platformInfoOffset+platformInfoHeader])
		data = binary.AppendUvarint(data, MaxABIStringBytesPerInput)
		data = append(data, strings.Repeat("v", MaxABIStringBytesPerInput)...)
		data = append(data, original[moduleOffset:]...)
		updatePlatformEdgeLoadSize(data)
		err := ValidateELFBinary(writePlatformELF(t, data), testOSLinux, testArchAMD64)
		require.ErrorIs(t, err, ErrInvalidELF)
		require.ErrorIs(t, err, ErrABIResourceLimit)
	})
	for _, header := range []struct {
		name   string
		offset int
	}{
		{name: "legacy version length", offset: platformEdgeHeaderAddr},
		{name: "legacy module length", offset: platformEdgeModuleAddr},
	} {
		t.Run(header.name, func(t *testing.T) {
			t.Parallel()
			data := legacyPlatformELF(t, platformEdgePointerSize, binary.LittleEndian, testOSLinux)
			binary.LittleEndian.PutUint64(data[header.offset+platformEdgePointerSize:], MaxABIStringBytesPerInput+1)
			err := ValidateELFBinary(writePlatformELF(t, data), testOSLinux, testArchAMD64)
			require.ErrorIs(t, err, ErrInvalidELF)
			require.ErrorIs(t, err, ErrABIResourceLimit)
		})
	}
}

// Repeated legacy headers can refer to one shared malformed module string. The
// file remains small while repeated ParseBuildInfo calls could allocate much
// more than its metadata; aggregate bounds must stop that work.
func TestGoELFPlatformRepeatedMalformedProbeBudgets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		osName    string
		duplicate int
		category  string
	}{
		{name: "shared strings", osName: strings.Repeat("a", MaxABIStringBytesPerInput/platformEdgeWordCount) + "\nbuild\tBROKEN",
			duplicate: platformEdgeWordCount, category: "strings"},
		{name: "shared records", osName: "linux\nbuild\tBROKEN", duplicate: MaxVersionRecordsPerInput/platformEdgeInfoLines + 1,
			category: "records"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := legacyPlatformELF(t, platformEdgePointerSize, binary.LittleEndian, tc.osName)
			header := bytes.Clone(data[platformInfoOffset : platformInfoOffset+platformInfoHeader])
			padding := (platformInfoAlign - len(data)%platformInfoAlign) % platformInfoAlign
			data = append(data, make([]byte, padding)...)
			for range tc.duplicate {
				data = append(data, header...)
			}
			updatePlatformEdgeLoadSize(data)
			err := ValidateELFBinary(writePlatformELF(t, data), testOSLinux, testArchAMD64)
			require.ErrorIs(t, err, ErrInvalidELF)
			require.ErrorIs(t, err, ErrABIResourceLimit)
			assert.Contains(t, err.Error(), tc.category)
		})
	}
}

func updatePlatformEdgeLoadSize(data []byte) {
	binary.LittleEndian.PutUint64(data[96:104], uint64(len(data)))
	binary.LittleEndian.PutUint64(data[104:112], uint64(len(data)))
}

func TestGoELFPlatformMisalignedProbeDoesNotHideLaterEvidence(t *testing.T) {
	t.Parallel()
	for _, osName := range []string{testOSLinux, platformEdgeOSFreeBSD} {
		t.Run(fmt.Sprintf("later %s target", osName), func(t *testing.T) {
			t.Parallel()
			data := inlinePlatformELF(t, "path\tapp\nbuild\tGOOS="+osName+"\n")
			// A misaligned magic before real linker data must not end the search.
			copy(data[platformInfoOffset-platformInfoHeader+1:], platformInfoMagic)
			err := ValidateELFBinary(writePlatformELF(t, data), testOSLinux, testArchAMD64)
			if osName == testOSLinux {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrInvalidELF)
				assert.Contains(t, err.Error(), "GOOS")
			}
		})
	}
}
