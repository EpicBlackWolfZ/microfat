package microarch_test

import (
	"fmt"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

// arm64LevelFixture defines a test fixture representing Microfat's ARM64 ISA compatibility model
// aligned with the Go GOARM64 target level hierarchy.
//
// Source of Truth & Compatibility Model:
// Microfat models ARM64 capabilities based on the Arm Architecture Reference Manual (ARM DDI 0487)
// ISA specifications and Linux auxiliary vector feature bits (AT_HWCAP / AT_HWCAP2), aligned with
// the Go toolchain's GOARM64 level hierarchy (v8.0 through v9.5).
// While the Go compiler (src/internal/buildcfg/cfg.go) accepts GOARM64 target levels and emits specific
// instructions (such as mandatory LSE atomics starting at v8.1), Microfat establishes a granular, forward-compatible
// hardware capability contract for every ISA milestone (including v8.8 MOPS/NMI/HBC and v8.9 GCS/THE)
// to guarantee that binaries built or specialized for these levels execute only on CPUs with verified hardware support.
type arm64LevelFixture struct {
	Level            string
	CompilerTarget   string
	Prereqs          []string
	RequiredFeatures []string
	BuildFeatures    func() microarch.ARM64Features
	SourceDoc        string
}

func getARM64CompatibilityFixtures() []arm64LevelFixture {
	return []arm64LevelFixture{
		{
			Level:            microarch.ARM64v8_0,
			CompilerTarget:   "GOARM64=v8.0",
			Prereqs:          nil,
			RequiredFeatures: nil,
			BuildFeatures: func() microarch.ARM64Features {
				return microarch.ARM64Features{HasFP: true, HasASIMD: true}
			},
			SourceDoc: "Go compiler GOARM64=v8.0: Baseline FP and Advanced SIMD (NEON)",
		},
		{
			Level:            microarch.ARM64v8_1,
			CompilerTarget:   "GOARM64=v8.1",
			Prereqs:          []string{microarch.ARM64v8_0},
			RequiredFeatures: []string{"atomics", "crc32"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{HasFP: true, HasASIMD: true}
				f.HasATOMICS = true
				f.HasCRC32 = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v8.1: v8.0 + Atomics (LSE) and CRC32 instructions",
		},
		{
			Level:            microarch.ARM64v8_2,
			CompilerTarget:   "GOARM64=v8.2",
			Prereqs:          []string{microarch.ARM64v8_1},
			RequiredFeatures: []string{"fphp", "asimdhp"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true}
				f.HasFPHP = true
				f.HasASIMDHP = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v8.2: v8.1 + Half-precision floating-point (FP16)",
		},
		{
			Level:            microarch.ARM64v8_3,
			CompilerTarget:   "GOARM64=v8.3",
			Prereqs:          []string{microarch.ARM64v8_2},
			RequiredFeatures: []string{"jscvt", "fcma", "lrcpc"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true,
				}
				f.HasJSCVT = true
				f.HasFCMA = true
				f.HasLRCPC = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v8.3: v8.2 + JavaScript conversion (JSCVT), complex number (FCMA), and RCpc",
		},
		{
			Level:            microarch.ARM64v8_4,
			CompilerTarget:   "GOARM64=v8.4",
			Prereqs:          []string{microarch.ARM64v8_3},
			RequiredFeatures: []string{"dcpop", "asimddp"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true, HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
				}
				f.HasDCPOP = true
				f.HasASIMDDP = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v8.4: v8.3 + DCPOP and ASIMDDP instructions",
		},
		{
			Level:            microarch.ARM64v8_5,
			CompilerTarget:   "GOARM64=v8.5",
			Prereqs:          []string{microarch.ARM64v8_4},
			RequiredFeatures: []string{"dit"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true, HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
					HasDCPOP: true, HasASIMDDP: true,
				}
				f.HasDIT = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v8.5: v8.4 + Data Independent Timing (DIT)",
		},
		{
			Level:            microarch.ARM64v8_6,
			CompilerTarget:   "GOARM64=v8.6",
			Prereqs:          []string{microarch.ARM64v8_5},
			RequiredFeatures: []string{"i8mm", "bf16"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true, HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
					HasDCPOP: true, HasASIMDDP: true, HasDIT: true,
				}
				f.HasI8MM = true
				f.HasBF16 = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v8.6: v8.5 + Int8 matrix multiplication (I8MM) and BFloat16 (BF16)",
		},
		{
			Level:            microarch.ARM64v8_7,
			CompilerTarget:   "GOARM64=v8.7",
			Prereqs:          []string{microarch.ARM64v8_6},
			RequiredFeatures: []string{"wfxt"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true, HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
					HasDCPOP: true, HasASIMDDP: true, HasDIT: true, HasI8MM: true, HasBF16: true,
				}
				f.HasWFxT = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v8.7: v8.6 + Wait For Event/Interrupt with Timeout instructions (WFxT)",
		},
		{
			Level:            microarch.ARM64v8_8,
			CompilerTarget:   "GOARM64=v8.8",
			Prereqs:          []string{microarch.ARM64v8_7},
			RequiredFeatures: []string{"mops", "nmi", "hbc"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true, HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
					HasDCPOP: true, HasASIMDDP: true, HasDIT: true, HasI8MM: true, HasBF16: true,
					HasWFxT: true,
				}
				f.HasMOPS = true
				f.HasNMI = true
				f.HasHBC = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v8.8: v8.7 + Memory Operations (MOPS), NMI, and Hinted Conditional Branches",
		},
		{
			Level:            microarch.ARM64v8_9,
			CompilerTarget:   "GOARM64=v8.9",
			Prereqs:          []string{microarch.ARM64v8_8},
			RequiredFeatures: []string{"gcs", "the"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true, HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
					HasDCPOP: true, HasASIMDDP: true, HasDIT: true, HasI8MM: true, HasBF16: true,
					HasWFxT: true, HasMOPS: true, HasNMI: true, HasHBC: true,
				}
				f.HasGCS = true
				f.HasTHE = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v8.9: v8.8 + Guarded Control Stack (GCS) and Translation Hardening (THE)",
		},
		{
			Level:            microarch.ARM64v9_0,
			CompilerTarget:   "GOARM64=v9.0",
			Prereqs:          []string{microarch.ARM64v8_5},
			RequiredFeatures: []string{"sve"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true, HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
					HasDCPOP: true, HasASIMDDP: true, HasDIT: true,
				}
				f.HasSVE = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v9.0: v8.5 + Scalable Vector Extension (SVE)",
		},
		{
			Level:            microarch.ARM64v9_1,
			CompilerTarget:   "GOARM64=v9.1",
			Prereqs:          []string{microarch.ARM64v9_0, microarch.ARM64v8_6},
			RequiredFeatures: nil,
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true, HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
					HasDCPOP: true, HasASIMDDP: true, HasDIT: true, HasI8MM: true, HasBF16: true,
				}
				f.HasSVE = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v9.1: v9.0 + v8.6 (includes SVE, I8MM, BF16, and DIT)",
		},
		{
			Level:            microarch.ARM64v9_2,
			CompilerTarget:   "GOARM64=v9.2",
			Prereqs:          []string{microarch.ARM64v9_1},
			RequiredFeatures: []string{"sve2"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true, HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
					HasDCPOP: true, HasASIMDDP: true, HasDIT: true, HasI8MM: true, HasBF16: true,
				}
				f.HasSVE = true
				f.HasSVE2 = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v9.2: v9.1 + Scalable Vector Extension 2 (SVE2)",
		},
		{
			Level:            microarch.ARM64v9_3,
			CompilerTarget:   "GOARM64=v9.3",
			Prereqs:          []string{microarch.ARM64v9_2},
			RequiredFeatures: []string{"sme"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true, HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
					HasDCPOP: true, HasASIMDDP: true, HasDIT: true, HasI8MM: true, HasBF16: true,
				}
				f.HasSVE = true
				f.HasSVE2 = true
				f.HasSME = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v9.3: v9.2 + Scalable Matrix Extension (SME)",
		},
		{
			Level:            microarch.ARM64v9_4,
			CompilerTarget:   "GOARM64=v9.4",
			Prereqs:          []string{microarch.ARM64v9_3},
			RequiredFeatures: nil,
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true, HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
					HasDCPOP: true, HasASIMDDP: true, HasDIT: true, HasI8MM: true, HasBF16: true,
				}
				f.HasSVE = true
				f.HasSVE2 = true
				f.HasSME = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v9.4: v9.3 baseline architecture level",
		},
		{
			Level:            microarch.ARM64v9_5,
			CompilerTarget:   "GOARM64=v9.5",
			Prereqs:          []string{microarch.ARM64v9_4},
			RequiredFeatures: []string{"sme2"},
			BuildFeatures: func() microarch.ARM64Features {
				f := microarch.ARM64Features{
					HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true,
					HasFPHP: true, HasASIMDHP: true, HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
					HasDCPOP: true, HasASIMDDP: true, HasDIT: true, HasI8MM: true, HasBF16: true,
				}
				f.HasSVE = true
				f.HasSVE2 = true
				f.HasSME = true
				f.HasSME2 = true
				return f
			},
			SourceDoc: "Go compiler GOARM64=v9.5: v9.4 + Scalable Matrix Extension 2 (SME2)",
		},
	}
}

func TestARM64Fixtures_EvaluationMatches(t *testing.T) {
	t.Parallel()

	fixtures := getARM64CompatibilityFixtures()
	const expectedCount = 16
	if len(fixtures) != expectedCount {
		t.Fatalf("expected %d authoritative fixtures, got %d", expectedCount, len(fixtures))
	}

	for _, fix := range fixtures {
		t.Run(fix.Level, func(t *testing.T) {
			t.Parallel()
			feat := fix.BuildFeatures()
			detected, statuses := microarch.EvaluateARM64Detailed(feat)
			expectedLevel := fix.Level
			if fix.Level == microarch.ARM64v9_3 {
				expectedLevel = microarch.ARM64v9_4 // v9.4 shares v9.3 requirements in Go compiler specification
			}
			if detected != expectedLevel {
				t.Errorf("detected %s, expected %s for %s", detected, expectedLevel, fix.CompilerTarget)
			}

			var matchedStatus *microarch.ARM64LevelStatus
			for i := range statuses {
				if statuses[i].Level == fix.Level {
					matchedStatus = &statuses[i]
					break
				}
			}
			if matchedStatus == nil {
				t.Fatalf("status for fixture level %s not found in Detailed evaluation", fix.Level)
			}
			if !matchedStatus.Satisfied {
				t.Errorf("expected level %s to be satisfied", fix.Level)
			}
			if len(matchedStatus.MissingFeatures) != 0 {
				t.Errorf("expected 0 missing features, got %v", matchedStatus.MissingFeatures)
			}
			if len(matchedStatus.MissingPrereqs) != 0 {
				t.Errorf("expected 0 missing prereqs, got %v", matchedStatus.MissingPrereqs)
			}
		})
	}
}

func TestARM64Fixtures_PrerequisiteDegradation(t *testing.T) {
	t.Parallel()

	fixtures := getARM64CompatibilityFixtures()
	for _, fix := range fixtures {
		if len(fix.RequiredFeatures) == 0 {
			continue
		}
		t.Run(fmt.Sprintf("%s_degradation", fix.Level), func(t *testing.T) {
			t.Parallel()
			for _, reqFeat := range fix.RequiredFeatures {
				feat := fix.BuildFeatures()
				switch reqFeat {
				case "fp":
					feat.HasFP = false
				case "asimd":
					feat.HasASIMD = false
				case "atomics":
					feat.HasATOMICS = false
				case "crc32":
					feat.HasCRC32 = false
				case "fphp":
					feat.HasFPHP = false
				case "asimdhp":
					feat.HasASIMDHP = false
				case "jscvt":
					feat.HasJSCVT = false
				case "fcma":
					feat.HasFCMA = false
				case "lrcpc":
					feat.HasLRCPC = false
				case "dcpop":
					feat.HasDCPOP = false
				case "asimddp":
					feat.HasASIMDDP = false
				case "dit":
					feat.HasDIT = false
				case "i8mm":
					feat.HasI8MM = false
				case "bf16":
					feat.HasBF16 = false
				case "wfxt":
					feat.HasWFxT = false
				case "mops":
					feat.HasMOPS = false
				case "nmi":
					feat.HasNMI = false
				case "hbc":
					feat.HasHBC = false
				case "gcs":
					feat.HasGCS = false
				case "the":
					feat.HasTHE = false
				case "sve":
					feat.HasSVE = false
				case "sve2":
					feat.HasSVE2 = false
				case "sme":
					feat.HasSME = false
				case "sme2":
					feat.HasSME2 = false
				}

				detected, _ := microarch.EvaluateARM64Detailed(feat)
				detectedRank := microarch.Rank(microarch.ArchARM64, detected)
				targetRank := microarch.Rank(microarch.ArchARM64, fix.Level)

				if detectedRank >= targetRank {
					t.Errorf("disabling required feature %q on level %s must degrade detected level below %s (got %s)",
						reqFeat, fix.Level, fix.Level, detected)
				}
			}
		})
	}
}

func TestARM64Fixtures_Monotonicity(t *testing.T) {
	t.Parallel()

	fixtures := getARM64CompatibilityFixtures()
	for i := 1; i < len(fixtures); i++ {
		prev := fixtures[i-1]
		curr := fixtures[i]

		prevRank := microarch.Rank(microarch.ArchARM64, prev.Level)
		currRank := microarch.Rank(microarch.ArchARM64, curr.Level)

		if currRank <= prevRank {
			t.Errorf("fixtures must exhibit strictly monotonic rank ordering: %s (rank %d) > %s (rank %d)",
				curr.Level, currRank, prev.Level, prevRank)
		}
	}
}
