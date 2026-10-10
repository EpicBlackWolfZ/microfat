package pack

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	platformBuildTimeout = 2 * time.Minute
	platformInfoOffset   = 256
	platformBaseAddress  = 0x400000
	platformInfoHeader   = 32
	platformInfoAlign    = 16
	platformInfoMagic    = "\xff Go buildinf:"
)

func TestPackTargetOS(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"", testOSLinux, "LINUX", "LiNuX", "freebsd", "windows", "darwin", "banana", " linux "} {
		for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
			t.Run(fmt.Sprintf("%q/format%d", target, version), func(t *testing.T) {
				t.Parallel()
				valid := target == "" || target == testOSLinux || target == "LINUX" || target == "LiNuX"
				for _, skip := range []bool{false, true} {
					dir := t.TempDir()
					opts := DefaultOptions()
					opts.TargetOS = target
					opts.FormatVersion = version
					opts.SkipELFValidation = skip
					opts.Compression = codec.AlgorithmNone
					opts.OutputPath = filepath.Join(dir, "output")
					opts.StubPath = filepath.Join(t.TempDir(), "missing")
					if valid {
						opts.StubPath = createDummyELF(t, t.TempDir(), "custom launcher", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
					}
					opts.Variants = map[string]string{"v1": opts.StubPath}
					original := []byte("preserve output")
					require.NoError(t, os.WriteFile(opts.OutputPath, original, inputSnapshotMode))
					idx, err := Pack(opts)
					if !valid {
						require.ErrorIs(t, err, ErrUnsupportedOS)
						assert.NotErrorIs(t, err, os.ErrNotExist)
						assert.Nil(t, idx)
						assertTargetArchOutputPreserved(t, opts.OutputPath, original)
						continue
					}
					require.NoError(t, err)
					assert.Equal(t, testOSLinux, idx.TargetOS)
					data, err := os.ReadFile(opts.OutputPath)
					require.NoError(t, err)
					stored, results, err := VerifyBinary(bytes.NewReader(data), int64(len(data)))
					require.NoError(t, err)
					assert.Equal(t, testOSLinux, stored.TargetOS)
					require.Len(t, results, 1)
					assert.True(t, results[0].Valid)
				}
			})
		}
	}
}

func TestValidateELFUnsupportedOSBeforeInput(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"freebsd", "windows", "darwin", "banana", " linux "} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			err := ValidateELFBinary(filepath.Join(t.TempDir(), "missing"), target, testArchAMD64)
			require.ErrorIs(t, err, ErrUnsupportedOS)
			assert.NotErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestELFOSABIPlatform(t *testing.T) {
	t.Parallel()
	for _, arch := range []struct {
		name    string
		machine elf.Machine
		level   string
	}{
		{testArchAMD64, elf.EM_X86_64, "v1"},
		{microarch.ArchARM64, elf.EM_AARCH64, testLevelV80},
	} {
		for _, abi := range []elf.OSABI{elf.ELFOSABI_NONE, elf.ELFOSABI_LINUX, elf.ELFOSABI_FREEBSD, elf.ELFOSABI_NETBSD,
			elf.ELFOSABI_SOLARIS, elf.ELFOSABI_OPENBSD, elf.OSABI(255)} {
			t.Run(fmt.Sprintf("%s/%s", arch.name, abi), func(t *testing.T) {
				t.Parallel()
				valid := abi == elf.ELFOSABI_NONE || abi == elf.ELFOSABI_LINUX
				good := createDummyELF(t, t.TempDir(), "good", uint16(arch.machine), byte(elf.ELFCLASS64))
				data, err := os.ReadFile(good)
				require.NoError(t, err)
				data[elf.EI_OSABI] = byte(abi)
				input := writePlatformELF(t, data)
				err = ValidateELFBinary(input, testOSLinux, arch.name)
				if valid {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, ErrInvalidELF)
					assert.Contains(t, err.Error(), "OSABI")
				}
				for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
					for _, role := range []string{"custom launcher", "variant"} {
						opts := DefaultOptions()
						opts.TargetArch = arch.name
						opts.FormatVersion = version
						opts.StubPath = good
						opts.Variants = map[string]string{arch.level: good}
						opts.OutputPath = filepath.Join(t.TempDir(), "output")
						if role == "custom launcher" {
							opts.StubPath = input
						} else {
							opts.Variants[arch.level] = input
						}
						original := []byte("preserve output")
						require.NoError(t, os.WriteFile(opts.OutputPath, original, inputSnapshotMode))
						_, err := Pack(opts)
						if valid {
							require.NoError(t, err)
						} else {
							require.ErrorIs(t, err, ErrInvalidELF)
							assertTargetArchOutputPreserved(t, opts.OutputPath, original)
						}
					}
				}
			})
		}
	}
}

func TestRealGoELFPlatformEvidence(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{testArchAMD64, microarch.ArchARM64} {
		for _, osName := range []string{testOSLinux, "freebsd"} {
			t.Run(osName+"/"+arch, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				source := filepath.Join(dir, "main.go")
				// Linux applications can embed foreign binaries in mutable asset data.
				// Only their own first linker block identifies the application target.
				foreign := inlinePlatformELF(t, "path\tasset\nbuild\tGOOS=freebsd\n")[platformInfoOffset:]
				foreign = append(foreign, make([]byte, platformInfoAlign-len(foreign)%platformInfoAlign)...)
				foreign = append(foreign, foreign...)
				program := fmt.Sprintf("package main\nvar asset = %#v\nfunc main() { asset[0]++; println(asset[32]) }\n", foreign)
				require.NoError(t, os.WriteFile(source, []byte(program), inputSnapshotMode))
				path := filepath.Join(dir, "app")
				ctx, cancel := context.WithTimeout(t.Context(), platformBuildTimeout)
				defer cancel()
				cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-ldflags=-s -w", "-o", path, source)
				cmd.Env = append(os.Environ(), "GOOS="+osName, "GOARCH="+arch, "CGO_ENABLED=0", "GOTOOLCHAIN=local")
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "building platform fixture: %s", output)
				info, err := buildinfo.ReadFile(path)
				require.NoError(t, err)
				found := false
				for _, setting := range info.Settings {
					if setting.Key == "GOOS" {
						require.Equal(t, osName, setting.Value)
						found = true
					}
				}
				require.True(t, found)
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				// Remove explicit OSABI evidence: FreeBSD must still be rejected using GOOS.
				data[elf.EI_OSABI] = byte(elf.ELFOSABI_NONE)
				for _, sections := range []bool{true, false} {
					if !sections {
						clear(data[40:48])
						clear(data[58:64])
					}
					err := ValidateELFBinary(writePlatformELF(t, data), testOSLinux, arch)
					if osName == testOSLinux {
						require.NoError(t, err)
					} else {
						require.ErrorIs(t, err, ErrInvalidELF)
						assert.Contains(t, err.Error(), "GOOS")
					}
				}
			})
		}
	}
}

func writePlatformELF(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input")
	require.NoError(t, os.WriteFile(path, data, inputSnapshotMode))
	return path
}

// A real file-backed segment carries inline linker metadata without section names.
func inlinePlatformELF(t *testing.T, module string) []byte {
	t.Helper()
	path := createDummyELF(t, t.TempDir(), "base", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	data = append(data, make([]byte, platformInfoOffset-len(data)+platformInfoHeader)...)
	copy(data[platformInfoOffset:], platformInfoMagic)
	data[platformInfoOffset+14] = 8
	data[platformInfoOffset+15] = 2
	data = binary.AppendUvarint(data, uint64(len("go1.27.2")))
	data = append(data, "go1.27.2"...)
	framed := "\x30\x77\xaf\x0c\x92\x74\x08\x02\x41\xe1\xc1\x07\xe6\xd6\x18\xe6" + module +
		"\xf9\x32\x43\x31\x86\x18\x20\x72\x00\x82\x42\x10\x41\x16\xd8\xf2"
	data = binary.AppendUvarint(data, uint64(len(framed)))
	data = append(data, framed...)
	binary.LittleEndian.PutUint32(data[68:72], uint32(elf.PF_R|elf.PF_W|elf.PF_X))
	binary.LittleEndian.PutUint64(data[96:104], uint64(len(data)))
	binary.LittleEndian.PutUint64(data[104:112], uint64(len(data)))
	return data
}

func TestGoELFPlatformMetadata(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		module string
		patch  func([]byte)
		failed bool
	}{
		{name: testOSLinux, module: "path\tapp\nbuild\tGOOS=linux\n"},
		{name: "freebsd with generic OSABI", module: "path\tapp\nbuild\tGOOS=freebsd\n", failed: true},
		{name: "windows with generic OSABI", module: "path\tapp\nbuild\tGOOS=windows\n", failed: true},
		{name: "quoted GOOS", module: "path\tapp\nbuild\tGOOS=\"freebsd\"\n", failed: true},
		{name: "duplicate conflicting GOOS", module: "path\tapp\nbuild\tGOOS=linux\nbuild\tGOOS=freebsd\n", failed: true},
		{name: "absent GOOS", module: "path\tapp\nbuild\tGOARCH=amd64\n"},
		{name: "GOOS text in dependency", module: "path\tapp\ndep\tGOOS=freebsd\tv1.0.0\tchecksum\n"},
		{name: "malformed module unavailable", module: "this is not valid module metadata\n"},
		{name: "malformed dependency columns", module: "path\tapp\ndep\t" + strings.Repeat("\t", 1024*1024) + "\n"},
		{name: "unframed metadata unavailable", module: "path\tapp\nbuild\tGOOS=freebsd\n",
			patch: func(data []byte) { data[len(data)-1] ^= 1 }},
		{name: "unknown pointer width unavailable", module: "path\tapp\nbuild\tGOOS=freebsd\n",
			patch: func(data []byte) { data[platformInfoOffset+14] = 3; data[platformInfoOffset+15] = 0 }},
		{name: "bad inline length unavailable", module: "path\tapp\nbuild\tGOOS=freebsd\n",
			patch: func(data []byte) { data[platformInfoOffset+platformInfoHeader] = 0xff }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := inlinePlatformELF(t, tc.module)
			if tc.patch != nil {
				tc.patch(data)
			}
			err := ValidateELFBinary(writePlatformELF(t, data), testOSLinux, testArchAMD64)
			if tc.failed {
				require.ErrorIs(t, err, ErrInvalidELF)
				assert.Contains(t, err.Error(), "GOOS")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGoELFPlatformMetadataBudgets(t *testing.T) {
	t.Parallel()
	for _, module := range []string{
		"path\t" + strings.Repeat("a", MaxABIStringBytesPerInput) + "\n",
		strings.Repeat("build\tGOOS=linux\n", MaxVersionRecordsPerInput+1),
	} {
		data := inlinePlatformELF(t, module)
		err := ValidateELFBinary(writePlatformELF(t, data), testOSLinux, testArchAMD64)
		require.ErrorIs(t, err, ErrInvalidELF)
		require.ErrorIs(t, err, ErrABIResourceLimit)
	}
}

func TestGoELFPlatformEvidencePreservesOutput(t *testing.T) {
	t.Parallel()
	good := createDummyELF(t, t.TempDir(), "good", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
	wrong := writePlatformELF(t, inlinePlatformELF(t, "path\tapp\nbuild\tGOOS=freebsd\n"))
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, role := range []string{"custom launcher", "variant"} {
			t.Run(fmt.Sprintf("format%d/%s", version, role), func(t *testing.T) {
				t.Parallel()
				opts := DefaultOptions()
				opts.FormatVersion = version
				opts.StubPath = good
				opts.Variants = map[string]string{"v1": good}
				opts.OutputPath = filepath.Join(t.TempDir(), "output")
				if role == "custom launcher" {
					opts.StubPath = wrong
				} else {
					opts.Variants["v1"] = wrong
				}
				original := []byte("preserve output")
				require.NoError(t, os.WriteFile(opts.OutputPath, original, inputSnapshotMode))
				_, err := Pack(opts)
				require.ErrorIs(t, err, ErrInvalidELF)
				assertTargetArchOutputPreserved(t, opts.OutputPath, original)
				opts.SkipELFValidation = true
				_, err = Pack(opts)
				require.NoError(t, err, "explicit header validation bypass remains available for supported targets")
			})
		}
	}
}

func TestLegacyGoELFPlatformMetadata(t *testing.T) {
	t.Parallel()
	for _, pointerSize := range []int{4, 8} {
		for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
			for _, osName := range []string{testOSLinux, "freebsd"} {
				t.Run(fmt.Sprintf("ptr%d/%s/%s", pointerSize, order, osName), func(t *testing.T) {
					t.Parallel()
					data := legacyPlatformELF(t, pointerSize, order, osName)
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
	}
}

func legacyPlatformELF(t *testing.T, pointerSize int, order binary.ByteOrder, osName string) []byte {
	t.Helper()
	inline := inlinePlatformELF(t, "path\tapp\nbuild\tGOOS="+osName+"\n")
	versionLen, prefix := binary.Uvarint(inline[platformInfoOffset+platformInfoHeader:])
	versionStart := platformInfoOffset + platformInfoHeader + prefix
	version := inline[versionStart : uint64(versionStart)+versionLen]
	moduleLengthOff := uint64(versionStart) + versionLen
	moduleLen, prefix := binary.Uvarint(inline[moduleLengthOff:])
	module := inline[moduleLengthOff+uint64(prefix) : moduleLengthOff+uint64(prefix)+moduleLen]
	data := bytes.Clone(inline[:platformInfoOffset+platformInfoHeader])
	data[platformInfoOffset+14] = byte(pointerSize)
	data[platformInfoOffset+15] = 0
	if order == binary.BigEndian {
		data[platformInfoOffset+15] = 1
	}
	versionHeader := len(data)
	moduleHeader := versionHeader + pointerSize*2
	versionStart = moduleHeader + pointerSize*2
	moduleStart := versionStart + len(version)
	data = append(data, make([]byte, pointerSize*4)...)
	data = append(data, version...)
	data = append(data, module...)
	putPointer := func(offset int, value uint64) {
		if pointerSize == 4 {
			order.PutUint32(data[offset:], uint32(value))
		} else {
			order.PutUint64(data[offset:], value)
		}
	}
	putPointer(platformInfoOffset+platformInfoAlign, uint64(platformBaseAddress+versionHeader))
	putPointer(platformInfoOffset+platformInfoAlign+pointerSize, uint64(platformBaseAddress+moduleHeader))
	putPointer(versionHeader, uint64(platformBaseAddress+versionStart))
	putPointer(versionHeader+pointerSize, uint64(len(version)))
	putPointer(moduleHeader, uint64(platformBaseAddress+moduleStart))
	putPointer(moduleHeader+pointerSize, uint64(len(module)))
	binary.LittleEndian.PutUint64(data[96:104], uint64(len(data)))
	binary.LittleEndian.PutUint64(data[104:112], uint64(len(data)))
	return data
}
