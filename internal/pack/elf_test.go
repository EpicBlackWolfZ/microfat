package pack

import (
	"debug/elf"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecutableELFStructure(t *testing.T) {
	t.Parallel()
	const base = 0x400000
	cases := []struct {
		name  string
		patch func([]byte)
		valid bool
	}{
		{"exec", func([]byte) {}, true},
		{"pie", func(b []byte) { binary.LittleEndian.PutUint16(b[16:18], uint16(elf.ET_DYN)) }, true},
		{"relocatable", func(b []byte) { binary.LittleEndian.PutUint16(b[16:18], uint16(elf.ET_REL)) }, false},
		{"core", func(b []byte) { binary.LittleEndian.PutUint16(b[16:18], uint16(elf.ET_CORE)) }, false},
		{"no load segment", func(b []byte) { binary.LittleEndian.PutUint32(b[64:68], uint32(elf.PT_NOTE)) }, false},
		{"nonexecutable", func(b []byte) { binary.LittleEndian.PutUint32(b[68:72], uint32(elf.PF_R)) }, false},
		{"zero entry", func(b []byte) { binary.LittleEndian.PutUint64(b[24:32], 0) }, false},
		{"entry below load", func(b []byte) { binary.LittleEndian.PutUint64(b[24:32], base-1) }, false},
		{"entry past load", func(b []byte) { binary.LittleEndian.PutUint64(b[24:32], base+uint64(len(b))) }, false},
		{"file exceeds memory", func(b []byte) { binary.LittleEndian.PutUint64(b[104:112], 0) }, false},
		{"file offset past EOF", func(b []byte) { binary.LittleEndian.PutUint64(b[72:80], uint64(len(b)+1)) }, false},
		{"file range past EOF", func(b []byte) { binary.LittleEndian.PutUint64(b[72:80], 1) }, false},
		{"virtual overflow", func(b []byte) { binary.LittleEndian.PutUint64(b[80:88], math.MaxUint64) }, false},
		{"nonpower alignment", func(b []byte) { b[112] = 3 }, false},
		{"inconsistent alignment", func(b []byte) { b[112] = 8; b[80] = 1 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			good := createDummyELF(t, dir, "good", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
			data, err := os.ReadFile(good)
			require.NoError(t, err)
			tc.patch(data)
			input := filepath.Join(dir, "input")
			require.NoError(t, os.WriteFile(input, data, 0o600))
			err = ValidateELFBinary(input, testOSLinux, testArchAMD64)
			if tc.valid {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrInvalidELF)
			for _, role := range []string{"stub", "variant"} {
				opts := DefaultOptions()
				opts.StubPath = good
				opts.Variants = map[string]string{"v1": good}
				opts.OutputPath = filepath.Join(dir, "output")
				if role == "stub" {
					opts.StubPath = input
				} else {
					opts.Variants["v1"] = input
				}
				require.NoError(t, os.WriteFile(opts.OutputPath, []byte("keep"), 0o600))
				_, err = Pack(opts)
				require.ErrorIs(t, err, ErrInvalidELF)
				got, readErr := os.ReadFile(opts.OutputPath)
				require.NoError(t, readErr)
				require.Equal(t, "keep", string(got))
				opts.SkipELFValidation = true
				_, err = Pack(opts)
				require.NoError(t, err, "explicit ELF bypass remains supported")
			}
		})
	}
}
