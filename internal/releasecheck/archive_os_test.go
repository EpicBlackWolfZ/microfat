package releasecheck_test

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testLinuxOS = "linux"
	testGOOS    = "GOOS"
)

func TestValidateArtifactLinuxTargetOS(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.3.0")
	require.NoError(t, err)
	for _, arch := range []string{releasecheck.ArchAMD64, releasecheck.ArchARM64} {
		for _, goos := range []string{"", "LINUX", "freebsd", "windows"} {
			t.Run(arch+"/"+goos, func(t *testing.T) {
				t.Parallel()
				entries := createStandardValidEntries(t, arch, contract)
				original := entries[0].Data
				idx, err := format.ReadTrailerAndIndex(bytes.NewReader(original), int64(len(original)))
				require.NoError(t, err)
				idx.TargetOS = goos
				indexOffset := binary.LittleEndian.Uint64(original[len(original)-format.TrailerSize:])
				var fat bytes.Buffer
				_, err = fat.Write(original[:indexOffset])
				require.NoError(t, err)
				_, err = format.WriteIndexAndTrailer(&fat, idx, int64(fat.Len()))
				require.NoError(t, err)
				entries[0].Data = fat.Bytes()
				archive := filepath.Join(t.TempDir(), contract.ExpectedArchives[arch])
				createOrderedTarGz(t, archive, entries)
				facts, err := releasecheck.ValidateArtifact(archive)
				require.ErrorContains(t, err, "target OS mismatch")
				assert.Nil(t, facts)
			})
		}
	}
}

func TestValidateArtifactLinuxExecutableOS(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.3.0")
	require.NoError(t, err)
	for _, arch := range []string{releasecheck.ArchAMD64, releasecheck.ArchARM64} {
		baseline := "v1"
		if arch == releasecheck.ArchARM64 {
			baseline = "v8.0"
		}
		foreign := getCompiledTestBinaryForOS(t, arch, baseline, "freebsd")
		for _, evidence := range []string{"foreign-osabi", "generic-osabi-foreign-goos", "conflicting-duplicate-goos"} {
			for _, product := range []string{
				releasecheck.ReleaseProjectName, releasecheck.ReleaseFullStub, releasecheck.ReleaseMinStub, "embedded",
			} {
				t.Run(arch+"/"+evidence+"/"+product, func(t *testing.T) {
					t.Parallel()
					entries := createStandardValidEntries(t, arch, contract)
					payload := bytes.Clone(foreign)
					const osabiOffset = 7
					expectedError := "ELF OSABI"
					if evidence == "generic-osabi-foreign-goos" {
						payload[osabiOffset] = byte(elf.ELFOSABI_NONE)
						expectedError = "GOOS setting mismatch"
					} else if evidence == "conflicting-duplicate-goos" {
						linux := getCompiledTestBinary(t, arch, baseline)
						payload = bytes.ReplaceAll(linux, []byte("GOARCH="+arch), []byte("GOOS=freebsd"))
						require.Len(t, payload, len(linux), "duplicate metadata mutation must preserve executable offsets")
						require.NotEqual(t, linux, payload)
						expectedError = "GOOS setting mismatch"
					}
					for i := range entries {
						if entries[i].Name != product {
							continue
						}
						if product == releasecheck.ReleaseProjectName {
							entries[i].Data = rebuildFatWithOSPayload(t, entries[i].Data, payload, false)
						} else {
							entries[i].Data = payload
						}
					}
					if product == "embedded" {
						entries[0].Data = rebuildFatWithOSPayload(t, entries[0].Data, payload, true)
					}
					archive := filepath.Join(t.TempDir(), contract.ExpectedArchives[arch])
					createOrderedTarGz(t, archive, entries)
					facts, err := releasecheck.ValidateArtifact(archive)
					require.ErrorContains(t, err, expectedError)
					assert.Nil(t, facts)
				})
			}
		}
	}
}

// Rebuild valid size, offsets and checksums around foreign executable bytes so the
// independent validator reaches the OS contract, rather than a producer or integrity rejection.
func rebuildFatWithOSPayload(t *testing.T, original, payload []byte, embedded bool) []byte {
	t.Helper()
	idx, err := format.ReadTrailerAndIndex(bytes.NewReader(original), int64(len(original)))
	require.NoError(t, err)
	var fat bytes.Buffer
	stub := original[:idx.Variants[0].Offset]
	if !embedded {
		stub = payload
	}
	_, err = fat.Write(stub)
	require.NoError(t, err)
	for i := range idx.Variants {
		v := &idx.Variants[i]
		require.Equal(t, "none", v.Compression)
		data := original[v.Offset : v.Offset+v.CompressedSize]
		if embedded && i == 0 {
			data = payload
		}
		v.Offset = int64(fat.Len())
		v.CompressedSize = int64(len(data))
		v.UncompressedSize = int64(len(data))
		v.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
		_, err = fat.Write(data)
		require.NoError(t, err)
	}
	_, err = format.WriteIndexAndTrailer(&fat, idx, int64(fat.Len()))
	require.NoError(t, err)
	return fat.Bytes()
}

func TestLinuxExecutableOSMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		osabi elf.OSABI
		goos  string
		valid bool
	}{
		{"sysv-linux", elf.ELFOSABI_NONE, testLinuxOS, true},
		{"linux-linux", elf.ELFOSABI_LINUX, testLinuxOS, true},
		{"foreign-osabi", elf.ELFOSABI_FREEBSD, testLinuxOS, false},
		{"foreign-goos", elf.ELFOSABI_NONE, "freebsd", false},
		{"missing-goos", elf.ELFOSABI_LINUX, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			header := &elf.FileHeader{Machine: elf.EM_X86_64, OSABI: tc.osabi}
			bi := &buildinfo.BuildInfo{}
			if tc.goos != "" {
				bi.Settings = []debug.BuildSetting{{Key: testGOOS, Value: tc.goos}}
			}
			rootErr := releasecheck.ValidateRootExecutableISA(releasecheck.ArchAMD64, "microfat", &releasecheck.ExecutableFacts{
				ELFHeader: header, BuildInfo: bi,
			})
			variantErr := releasecheck.ValidateEmbeddedVariantISA(releasecheck.ArchAMD64, "v1", &releasecheck.VariantFacts{
				ELFHeader: header, BuildInfo: bi,
			})
			if tc.valid {
				require.NoError(t, rootErr)
				require.NoError(t, variantErr)
			} else {
				require.Error(t, rootErr)
				require.Error(t, variantErr)
			}
		})
	}
}

func TestLinuxExecutableDuplicateOSMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		values []string
		valid  bool
	}{
		{"linux-before-freebsd", []string{testLinuxOS, "freebsd"}, false},
		{"freebsd-before-linux", []string{"freebsd", testLinuxOS}, false},
		{"consistent-linux", []string{testLinuxOS, testLinuxOS}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			header := &elf.FileHeader{Machine: elf.EM_X86_64}
			bi := &buildinfo.BuildInfo{}
			for _, value := range tc.values {
				bi.Settings = append(bi.Settings, debug.BuildSetting{Key: testGOOS, Value: value})
			}
			rootErr := releasecheck.ValidateRootExecutableISA(releasecheck.ArchAMD64, "microfat", &releasecheck.ExecutableFacts{
				ELFHeader: header, BuildInfo: bi,
			})
			variantErr := releasecheck.ValidateEmbeddedVariantISA(releasecheck.ArchAMD64, "v1", &releasecheck.VariantFacts{
				ELFHeader: header, BuildInfo: bi,
			})
			if tc.valid {
				require.NoError(t, rootErr)
				require.NoError(t, variantErr)
			} else {
				require.ErrorContains(t, rootErr, "GOOS setting mismatch")
				require.ErrorContains(t, variantErr, "GOOS setting mismatch")
			}
		})
	}
}
