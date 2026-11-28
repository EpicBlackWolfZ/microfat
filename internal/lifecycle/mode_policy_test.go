package lifecycle

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModeAndOwnershipPolicyContracts(t *testing.T) {
	t.Parallel()

	require.True(t, resolveOwnershipPreservation(true, true, false))
	require.False(t, resolveOwnershipPreservation(true, false, false))
	require.True(t, resolveOwnershipPreservation(true, false, true))
	require.False(t, resolveOwnershipPreservation(false, true, true))

	perm, sticky := computeTargetModeBits(0o4755, true, true, false)
	require.Equal(t, uint32(0o755), perm)
	require.True(t, sticky)

	// Exhaustively prove equivalence against Go bitwise PolicyStrip mode calculation over 0..0o777.
	for mode := range uint32(0o1000) {
		expected := mode & standardExecMask
		if expected&ownerExecuteMask == 0 {
			expected |= standardExecMask
		}
		got, gotSticky := computeTargetModeBits(mode|0o6000, true, true, true)
		require.Equal(t, expected, got, "mode %04o", mode)
		require.False(t, gotSticky)
	}
}
