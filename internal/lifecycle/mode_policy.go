package lifecycle

const (
	lifecycleStandardExecMask uint32 = 0o755
	lifecyclePermMask         uint32 = 0o777
	lifecycleOctalBase        uint32 = 8
	lifecycleOwnerDiv         uint32 = 64
	lifecycleReadBit          uint32 = 4
)

/*@
ghost
decreases
pure func triadHighZeroAndLowPreserved(t uint32) bool {
	return t == 0 || t == 1 || t == 4 || t == 5
}
@*/

// resolveOwnershipPreservation enforces that IntentCreateOnly destinations do not copy
// source ownership unless PolicyStrict was explicitly requested.
// @ ensures preserve == (isStrict && (isReplaceSource || explicitPolicy))
// @ ensures !isStrict ==> !preserve
// @ ensures (!isReplaceSource && !explicitPolicy) ==> !preserve
// @ decreases
func resolveOwnershipPreservation(isStrict, isReplaceSource, explicitPolicy bool) /*@ (preserve @*/ bool /*@ ) @*/ {
	return isStrict && (isReplaceSource || explicitPolicy)
}

// stripRxOnlyTriad maps a POSIX rwx triad [0..7] to r-x [0,1,4,5] without bitmask overflow alerts.
// @ requires t <= 7
// @ ensures triadHighZeroAndLowPreserved(out)
// @ ensures out <= 5
// @ decreases
func stripRxOnlyTriad(t uint32) /*@ (out @*/ uint32 /*@ ) @*/ {
	res := uint32(0)
	if t >= lifecycleReadBit {
		res += lifecycleReadBit
	}
	if t%2 == 1 {
		res++
	}
	return res
}

// computeTargetModeBits normalizes destination POSIX permission bits and sticky-bit retention:
// setuid/setgid bits are always cleared, PolicyStrip clears sticky & group/world write bits
// and ensures owner execute (fallback 0755), and non-strip policies mask to 0777.
// @ ensures targetPerm <= lifecyclePermMask
// @ ensures isStrip ==> !keepSticky && ((targetPerm / lifecycleOwnerDiv) % 2) == 1
// @ ensures isStrip ==> triadHighZeroAndLowPreserved((targetPerm / lifecycleOctalBase) % lifecycleOctalBase)
// @ ensures isStrip ==> triadHighZeroAndLowPreserved(targetPerm % lifecycleOctalBase)
// @ ensures !isStrip ==> keepSticky == (isStrict && hasSticky)
// @ decreases
func computeTargetModeBits(perm uint32, hasSticky, isStrict, isStrip bool) (
	/*@ targetPerm @*/ uint32,
	/*@ keepSticky @*/ bool,
) {
	cleanPerm := perm % (lifecyclePermMask + 1)
	if isStrip {
		owner := (cleanPerm / lifecycleOwnerDiv) % lifecycleOctalBase
		// Explicit odd-triad check avoids nonlinear SMT modular-division ambiguity in Gobra.
		if owner != 1 && owner != 3 && owner != 5 && owner != 7 {
			return lifecycleStandardExecMask, false
		}
		group := stripRxOnlyTriad((cleanPerm / lifecycleOctalBase) % lifecycleOctalBase)
		other := stripRxOnlyTriad(cleanPerm % lifecycleOctalBase)
		tail := group*lifecycleOctalBase + other
		res := owner*lifecycleOwnerDiv + tail
		return res, false
	}
	return cleanPerm, isStrict && hasSticky
}
