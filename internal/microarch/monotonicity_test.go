package microarch_test

import (
	"math/rand/v2"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/testutil"
)

const (
	propMicroarchIters = 50
	amd64FeatureCount  = 20
	arm64FeatureCount  = 30
)

func TestMicroarch_AMD64MonotonicityInvariants(t *testing.T) {
	t.Parallel()

	testutil.RunPropertyTest(t, "AMD64_Monotonicity", propMicroarchIters, 0, func(subT *testing.T, iter int, rng *rand.Rand) {
		feat := microarch.X86Features{
			HasCX16:     rng.IntN(2) == 1,
			HasPOPCNT:   rng.IntN(2) == 1,
			HasSSE3:     rng.IntN(2) == 1,
			HasSSSE3:    rng.IntN(2) == 1,
			HasSSE41:    rng.IntN(2) == 1,
			HasSSE42:    rng.IntN(2) == 1,
			HasAVX:      rng.IntN(2) == 1,
			HasAVX2:     rng.IntN(2) == 1,
			HasBMI1:     rng.IntN(2) == 1,
			HasBMI2:     rng.IntN(2) == 1,
			HasFMA:      rng.IntN(2) == 1,
			HasOSXSAVE:  rng.IntN(2) == 1,
			HasF16C:     rng.IntN(2) == 1,
			HasLZCNT:    rng.IntN(2) == 1,
			HasMOVBE:    rng.IntN(2) == 1,
			HasAVX512F:  rng.IntN(2) == 1,
			HasAVX512BW: rng.IntN(2) == 1,
			HasAVX512CD: rng.IntN(2) == 1,
			HasAVX512DQ: rng.IntN(2) == 1,
			HasAVX512VL: rng.IntN(2) == 1,
		}

		detected := microarch.EvaluateAMD64(feat)
		detectedRank := microarch.Rank(microarch.ArchAMD64, detected)

		// Invariant 1: Detected rank must always be at least v1
		if detectedRank < microarch.Rank(microarch.ArchAMD64, microarch.AMD64v1) {
			subT.Fatalf("detected rank %d is below baseline v1 rank", detectedRank)
		}

		// Invariant 2: If v4 is detected, all prerequisite features of v2, v3, and v4 must be true
		if detected == microarch.AMD64v4 {
			if !feat.HasAVX512F || !feat.HasAVX512BW || !feat.HasAVX512CD || !feat.HasAVX512DQ || !feat.HasAVX512VL {
				subT.Fatalf("detected v4 but missing AVX-512 features")
			}
			if !feat.HasAVX || !feat.HasAVX2 || !feat.HasBMI1 || !feat.HasBMI2 || !feat.HasFMA {
				subT.Fatalf("detected v4 but missing v3 features")
			}
			if !feat.HasCX16 || !feat.HasPOPCNT || !feat.HasSSE3 || !feat.HasSSE41 || !feat.HasSSE42 {
				subT.Fatalf("detected v4 but missing v2 features")
			}
		}

		// Invariant 3: If v3 is detected, all v2 and v3 features must be true
		if detected == microarch.AMD64v3 {
			if !feat.HasAVX || !feat.HasAVX2 || !feat.HasBMI1 || !feat.HasBMI2 || !feat.HasFMA {
				subT.Fatalf("detected v3 but missing v3 features")
			}
			if !feat.HasCX16 || !feat.HasPOPCNT || !feat.HasSSE3 || !feat.HasSSE41 || !feat.HasSSE42 {
				subT.Fatalf("detected v3 but missing v2 features")
			}
		}

		// Invariant 4: If v2 is detected, all v2 features must be true
		if detected == microarch.AMD64v2 {
			if !feat.HasCX16 || !feat.HasPOPCNT || !feat.HasSSE3 || !feat.HasSSE41 || !feat.HasSSE42 {
				subT.Fatalf("detected v2 but missing v2 features")
			}
		}
	})
}

func TestMicroarch_ARM64MonotonicityInvariants(t *testing.T) {
	t.Parallel()

	testutil.RunPropertyTest(t, "ARM64_Monotonicity", propMicroarchIters, 0, func(subT *testing.T, iter int, rng *rand.Rand) {
		feat := microarch.ARM64Features{
			HasFP:       rng.IntN(2) == 1,
			HasASIMD:    rng.IntN(2) == 1,
			HasATOMICS:  rng.IntN(2) == 1,
			HasCRC32:    rng.IntN(2) == 1,
			HasFPHP:     rng.IntN(2) == 1,
			HasASIMDHP:  rng.IntN(2) == 1,
			HasJSCVT:    rng.IntN(2) == 1,
			HasFCMA:     rng.IntN(2) == 1,
			HasLRCPC:    rng.IntN(2) == 1,
			HasDCPOP:    rng.IntN(2) == 1,
			HasASIMDDP:  rng.IntN(2) == 1,
			HasDIT:      rng.IntN(2) == 1,
			HasSVE:      rng.IntN(2) == 1,
			HasSVE2:     rng.IntN(2) == 1,
			HasI8MM:     rng.IntN(2) == 1,
			HasBF16:     rng.IntN(2) == 1,
			HasWFxT:     rng.IntN(2) == 1,
			HasSME:      rng.IntN(2) == 1,
			HasSME2:     rng.IntN(2) == 1,
			HasAES:      rng.IntN(2) == 1,
			HasPMULL:    rng.IntN(2) == 1,
			HasSHA1:     rng.IntN(2) == 1,
			HasSHA2:     rng.IntN(2) == 1,
			HasSHA3:     rng.IntN(2) == 1,
			HasSHA512:   rng.IntN(2) == 1,
			HasSM3:      rng.IntN(2) == 1,
			HasSM4:      rng.IntN(2) == 1,
			HasASIMDFHM: rng.IntN(2) == 1,
			HasASIMDRDM: rng.IntN(2) == 1,
		}

		detected, statuses := microarch.EvaluateARM64Detailed(feat)
		detectedRank := microarch.Rank(microarch.ArchARM64, detected)

		// Invariant 1: Detected rank must always be at least v8.0
		if detectedRank < microarch.Rank(microarch.ArchARM64, microarch.ARM64v8_0) {
			subT.Fatalf("detected rank %d below ARM64 baseline rank", detectedRank)
		}

		// Invariant 2: For every satisfied level, all of its declared prerequisite levels must be satisfied
		for _, s := range statuses {
			if s.Satisfied {
				for _, prereq := range s.Prereqs {
					for _, other := range statuses {
						if other.Level == prereq && !other.Satisfied {
							subT.Fatalf("level %s satisfied but prerequisite %s is not satisfied", s.Level, prereq)
						}
					}
				}
			}
		}
	})
}

func TestMicroarch_PolicyPropertyInvariants(t *testing.T) {
	t.Parallel()

	amd64Levels := []string{microarch.AMD64v1, microarch.AMD64v2, microarch.AMD64v3, microarch.AMD64v4}

	testutil.RunPropertyTest(t, "Policy_Invariants", propMicroarchIters, 0, func(subT *testing.T, iter int, rng *rand.Rand) {
		hostLevel := amd64Levels[rng.IntN(len(amd64Levels))]
		hostRank := microarch.Rank(microarch.ArchAMD64, hostLevel)

		// Always ensure baseline is embedded so a compatible variant exists
		embedded := []string{microarch.AMD64v1}
		for _, lvl := range amd64Levels[1:] {
			if rng.IntN(2) == 1 {
				embedded = append(embedded, lvl)
			}
		}

		// Random policy settings
		var p microarch.Policy
		if rng.IntN(2) == 1 {
			maxLvl := amd64Levels[rng.IntN(len(amd64Levels))]
			p.MaxLevel = maxLvl
		}
		if rng.IntN(3) == 1 {
			// Disable a higher level
			p.DisabledVariants = []string{microarch.AMD64v4}
		}

		res, err := microarch.SelectVariantWithPolicy(microarch.ArchAMD64, hostLevel, embedded, p)
		if err != nil {
			subT.Fatalf("expected successful variant selection since v1 is embedded: %v", err)
		}

		selectedRank := microarch.Rank(microarch.ArchAMD64, res.SelectedVariant)

		// Invariant 1: Selected rank must never exceed host rank
		if selectedRank > hostRank {
			subT.Fatalf("selected rank %d exceeds host rank %d", selectedRank, hostRank)
		}

		// Invariant 2: If MaxLevel is set, selected rank must never exceed max level rank
		if p.MaxLevel != "" {
			maxRank := microarch.Rank(microarch.ArchAMD64, p.MaxLevel)
			if maxRank >= 0 && selectedRank > maxRank {
				subT.Fatalf("selected rank %d exceeds max level rank %d", selectedRank, maxRank)
			}
		}

		// Invariant 3: Selected variant must never be in disabled list
		for _, disabled := range p.DisabledVariants {
			if res.SelectedVariant == disabled {
				subT.Fatalf("selected variant %s was in disabled variants list %v", res.SelectedVariant, p.DisabledVariants)
			}
		}
	})
}
