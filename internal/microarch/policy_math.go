package microarch

const (
	policyReasonNone      = 0
	policyReasonMaxLevel  = 1
	policyReasonAVX512Cap = 2

	minAMD64Rank = 1
	maxAMD64Rank = 4
)

/*@
ghost
decreases
pure func capWithExplicitMax(hostRank, explicitMaxRank int, hasExplicitMax bool) int {
	return (hasExplicitMax && explicitMaxRank >= 0 && explicitMaxRank < hostRank) ? explicitMaxRank : hostRank
}

ghost
decreases
pure func capWithAVX512(curMax int, applyAVX512Cap bool, avx512CapRank int) int {
	return (applyAVX512Cap && avx512CapRank >= 0 && avx512CapRank < curMax) ? avx512CapRank : curMax
}
@*/

// effectivePolicyMaxRank computes the tightest upper rank limit allowed by host rank,
// explicit MICROFAT_MAX_LEVEL, and AVX-512 downclock protection along with the reason code.
// @ requires hostRank >= 0
// @ ensures effectiveRank == capWithAVX512(capWithExplicitMax(hostRank, explicitMaxRank, hasExplicitMax), applyAVX512Cap, avx512CapRank)
// @ ensures effectiveRank <= hostRank && 0 <= effectiveRank
// @ ensures hasExplicitMax && explicitMaxRank >= 0 ==> effectiveRank <= explicitMaxRank
// @ ensures applyAVX512Cap && avx512CapRank >= 0 ==> effectiveRank <= avx512CapRank
// @ ensures reasonCode == policyReasonNone || reasonCode == policyReasonMaxLevel || reasonCode == policyReasonAVX512Cap
// @ decreases
func effectivePolicyMaxRank(
	hostRank, explicitMaxRank int,
	hasExplicitMax, applyAVX512Cap bool,
	avx512CapRank int,
) (
	/*@ effectiveRank @*/ int,
	/*@ reasonCode @*/ int,
) {
	effectiveRank := hostRank
	reasonCode := policyReasonNone

	if hasExplicitMax && explicitMaxRank >= 0 && explicitMaxRank < effectiveRank {
		effectiveRank = explicitMaxRank
		reasonCode = policyReasonMaxLevel
	}
	if applyAVX512Cap && avx512CapRank >= 0 && avx512CapRank < effectiveRank {
		effectiveRank = avx512CapRank
		reasonCode = policyReasonAVX512Cap
	}
	return effectiveRank, reasonCode
}

// candidateAllowedByPolicy checks rank validity, host compatibility ceiling, policy ceiling, and blacklist state.
// @ ensures allowed == (0 <= rank && rank <= hostRank && rank <= effectiveMaxRank && !disabled)
// @ decreases
func candidateAllowedByPolicy(rank, hostRank, effectiveMaxRank int, disabled bool) /*@ (allowed @*/ bool /*@ ) @*/ {
	if rank < 0 || rank > hostRank || rank > effectiveMaxRank || disabled {
		return false
	}
	return true
}

// amd64RankSatisfied reports whether a normalized AMD64 candidate rank satisfies host hardware support.
// @ ensures ok == (minAMD64Rank <= candidateRank && candidateRank <= maxAMD64Rank && candidateRank <= hostRank)
// @ decreases
func amd64RankSatisfied(candidateRank, hostRank int) /*@ (ok @*/ bool /*@ ) @*/ {
	return candidateRank >= minAMD64Rank && candidateRank <= maxAMD64Rank && candidateRank <= hostRank
}
