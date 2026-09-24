package pack

import (
	"debug/elf"
	"testing"
)

func FuzzInspectELFABI(f *testing.F) {
	// Seed 1: Minimal valid 64-bit AMD64 ELF (Static EXEC)
	staticELF := buildSyntheticELF(f, syntheticELFOpts{
		eType: uint16(elf.ET_EXEC),
		entry: 0x400040,
	})
	f.Add(staticELF)

	// Seed 2: Minimal 64-bit AMD64 PIE (ET_DYN)
	pieELF := buildSyntheticELF(f, syntheticELFOpts{
		eType: uint16(elf.ET_DYN),
		entry: 0x400040,
	})
	f.Add(pieELF)

	// Seed 3: Synthetic dynamic ELF with interpreter, dependencies, and versions
	dynELF := buildSyntheticELF(f, syntheticELFOpts{
		eType:        uint16(elf.ET_DYN),
		entry:        0x400040,
		interp:       testLdLinux,
		dependencies: []string{testLibc, testLibm},
		verneeds: []syntheticVerneed{
			{
				file: testLibc,
				versions: []syntheticVernaux{
					{name: testGlibc225},
					{name: "GLIBC_2.34"},
				},
			},
		},
	})
	f.Add(dynELF)

	// Seed 4: Corrupt / truncated / garbage inputs
	f.Add([]byte{})
	f.Add([]byte("not an elf binary"))
	f.Add([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	f.Add(make([]byte, 256))

	f.Fuzz(func(t *testing.T, elfData []byte) {
		// InspectELFABI must handle any arbitrary byte slice without panicking
		report, err := InspectELFABI(elfData)
		if err != nil {
			// Expected for invalid ELF or malformed ABI structures
			return
		}

		if report == nil {
			t.Fatalf("expected non-nil report when err is nil")
		}

		report.Level = "v1"
		v2 := *report
		v2.Level = "v2"

		// Self-comparison should succeed without panic
		_, _ = CompareVariantABIs([]*VariantABIReport{report, &v2}, false)
		_, _ = CompareVariantABIs([]*VariantABIReport{report, &v2}, true)
	})
}
