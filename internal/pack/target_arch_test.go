package pack

import (
	"bytes"
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const targetArchBaselineAMD64 = "v1"

type targetArchTestCase struct {
	name      string
	target    string
	canonical string
	level     string
	machine   elf.Machine
	wrong     elf.Machine
}

func targetArchTestCases() []targetArchTestCase {
	return []targetArchTestCase{
		{"amd64", testArchAMD64, microarch.ArchAMD64, targetArchBaselineAMD64, elf.EM_X86_64, elf.EM_AARCH64},
		{"amd64 uppercase", "AMD64", microarch.ArchAMD64, targetArchBaselineAMD64, elf.EM_X86_64, elf.EM_AARCH64},
		{"amd64 mixed case", "AmD64", microarch.ArchAMD64, targetArchBaselineAMD64, elf.EM_X86_64, elf.EM_AARCH64},
		{"x86 underscore alias", "x86_64", microarch.ArchAMD64, targetArchBaselineAMD64, elf.EM_X86_64, elf.EM_AARCH64},
		{"x86 underscore uppercase", "X86_64", microarch.ArchAMD64, targetArchBaselineAMD64, elf.EM_X86_64, elf.EM_AARCH64},
		{"x86 hyphen alias", "x86-64", microarch.ArchAMD64, targetArchBaselineAMD64, elf.EM_X86_64, elf.EM_AARCH64},
		{"x86 hyphen uppercase", "X86-64", microarch.ArchAMD64, targetArchBaselineAMD64, elf.EM_X86_64, elf.EM_AARCH64},
		{microarch.ArchARM64, microarch.ArchARM64, microarch.ArchARM64, testLevelV80, elf.EM_AARCH64, elf.EM_X86_64},
		{"arm64 uppercase", "ARM64", microarch.ArchARM64, testLevelV80, elf.EM_AARCH64, elf.EM_X86_64},
		{"arm64 mixed case", "Arm64", microarch.ArchARM64, testLevelV80, elf.EM_AARCH64, elf.EM_X86_64},
		{"aarch64 alias", "aarch64", microarch.ArchARM64, testLevelV80, elf.EM_AARCH64, elf.EM_X86_64},
		{"aarch64 uppercase", "AARCH64", microarch.ArchARM64, testLevelV80, elf.EM_AARCH64, elf.EM_X86_64},
	}
}

func TestValidateELFBinary_TargetArchitecture(t *testing.T) {
	t.Parallel()
	for _, tc := range targetArchTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for _, machineCase := range []struct {
				name    string
				machine elf.Machine
				valid   bool
			}{
				{"matching machine", tc.machine, true},
				{"wrong machine", tc.wrong, false},
			} {
				t.Run(machineCase.name, func(t *testing.T) {
					t.Parallel()
					input := createDummyELF(t, dir, machineCase.name, uint16(machineCase.machine), byte(elf.ELFCLASS64))
					err := ValidateELFBinary(input, testOSLinux, tc.target)
					if machineCase.valid {
						require.NoError(t, err)
						return
					}
					require.ErrorIs(t, err, ErrInvalidELF)
				})
			}
		})
	}
}

func TestValidateELFBinary_UnsupportedTargetArchitecture(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"", "mips64", "386", "arm", "unsupported"} {
		t.Run(fmt.Sprintf("target %q before missing input", target), func(t *testing.T) {
			t.Parallel()
			missing := filepath.Join(t.TempDir(), "missing-elf")
			err := ValidateELFBinary(missing, testOSLinux, target)
			require.ErrorIs(t, err, ErrUnsupportedArch)
			assert.NotErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestPack_CanonicalTargetArchitecture(t *testing.T) {
	t.Parallel()
	cases := append(targetArchTestCases(), targetArchTestCase{
		name:      "empty defaults to amd64",
		canonical: microarch.ArchAMD64,
		level:     targetArchBaselineAMD64,
		machine:   elf.EM_X86_64,
	})
	for _, tc := range cases {
		for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
			t.Run(fmt.Sprintf("%s/format v%d", tc.name, version), func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				opts := DefaultOptions()
				opts.TargetArch = tc.target
				opts.FormatVersion = version
				opts.Compression = codec.AlgorithmNone
				opts.StubPath = createDummyELF(t, dir, "launcher", uint16(tc.machine), byte(elf.ELFCLASS64))
				opts.Variants = map[string]string{
					tc.level: createDummyELF(t, dir, "payload", uint16(tc.machine), byte(elf.ELFCLASS64)),
				}
				opts.OutputPath = filepath.Join(dir, "fat-app")

				idx, err := Pack(opts)
				require.NoError(t, err)
				assert.Equal(t, tc.canonical, idx.TargetArch)
				assert.Equal(t, version, idx.Version)

				data, err := os.ReadFile(opts.OutputPath)
				require.NoError(t, err)
				stored, err := format.ReadTrailerAndIndex(bytes.NewReader(data), int64(len(data)))
				require.NoError(t, err)
				assert.Equal(t, tc.canonical, stored.TargetArch)
				assert.Equal(t, version, stored.Version)

				verified, results, err := VerifyBinary(bytes.NewReader(data), int64(len(data)))
				require.NoError(t, err)
				assert.Equal(t, tc.canonical, verified.TargetArch)
				require.Len(t, results, len(opts.Variants))
				assert.True(t, results[0].Valid)
				assert.NoError(t, results[0].Error)
			})
		}
	}
}

func TestPack_TargetArchitectureMismatchPreservesOutput(t *testing.T) {
	t.Parallel()
	for _, tc := range targetArchTestCases() {
		for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
			for _, role := range []string{"launcher", "payload"} {
				t.Run(fmt.Sprintf("%s/format v%d/wrong %s", tc.name, version, role), func(t *testing.T) {
					t.Parallel()
					// Keep inputs outside the destination to make any output residue visible.
					inputDir := t.TempDir()
					outputDir := t.TempDir()
					good := createDummyELF(t, inputDir, "good", uint16(tc.machine), byte(elf.ELFCLASS64))
					wrong := createDummyELF(t, inputDir, "wrong", uint16(tc.wrong), byte(elf.ELFCLASS64))
					opts := DefaultOptions()
					opts.TargetArch = tc.target
					opts.FormatVersion = version
					opts.StubPath = good
					opts.Variants = map[string]string{tc.level: good}
					opts.OutputPath = filepath.Join(outputDir, "fat-app")
					if role == "launcher" {
						opts.StubPath = wrong
					} else {
						opts.Variants[tc.level] = wrong
					}
					original := []byte("preserve existing output")
					require.NoError(t, os.WriteFile(opts.OutputPath, original, inputSnapshotMode))

					idx, err := Pack(opts)
					require.ErrorIs(t, err, ErrInvalidELF)
					assert.Nil(t, idx)
					assertTargetArchOutputPreserved(t, opts.OutputPath, original)
				})
			}
		}
	}
}

func TestPack_UnsupportedTargetArchitectureBeforeInputs(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"mips64", "386", "arm", "unsupported"} {
		for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
			for _, skipValidation := range []bool{false, true} {
				name := fmt.Sprintf("%s/format v%d/skip ELF validation %t", target, version, skipValidation)
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					inputDir := t.TempDir()
					outputDir := t.TempDir()
					opts := DefaultOptions()
					opts.TargetArch = target
					opts.FormatVersion = version
					opts.SkipELFValidation = skipValidation
					opts.StubPath = filepath.Join(inputDir, "missing-stub")
					opts.Variants = map[string]string{targetArchBaselineAMD64: filepath.Join(inputDir, "missing-payload")}
					opts.OutputPath = filepath.Join(outputDir, "fat-app")
					original := []byte("preserve existing output")
					require.NoError(t, os.WriteFile(opts.OutputPath, original, inputSnapshotMode))

					idx, err := Pack(opts)
					require.ErrorIs(t, err, ErrUnsupportedArch)
					assert.NotErrorIs(t, err, os.ErrNotExist)
					assert.Nil(t, idx)
					assertTargetArchOutputPreserved(t, opts.OutputPath, original)
				})
			}
		}
	}
}

func assertTargetArchOutputPreserved(t *testing.T, outputPath string, original []byte) {
	t.Helper()
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Equal(t, original, data)
	entries, err := os.ReadDir(filepath.Dir(outputPath))
	require.NoError(t, err)
	require.Len(t, entries, 1, "failed packaging must leave no destination temporary files")
	assert.Equal(t, filepath.Base(outputPath), entries[0].Name())
}
