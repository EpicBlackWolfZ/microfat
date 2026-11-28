package microarch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPolicyMathContracts(t *testing.T) {
	t.Parallel()

	eff, reason := effectivePolicyMaxRank(rankAMD64v4, rankAMD64v2, true, false, rankAMD64v3)
	require.Equal(t, rankAMD64v2, eff)
	require.Equal(t, policyReasonMaxLevel, reason)

	eff, reason = effectivePolicyMaxRank(rankAMD64v4, -1, false, true, rankAMD64v3)
	require.Equal(t, rankAMD64v3, eff)
	require.Equal(t, policyReasonAVX512Cap, reason)

	eff, reason = effectivePolicyMaxRank(rankAMD64v3, rankAMD64v4, true, false, rankAMD64v3)
	require.Equal(t, rankAMD64v3, eff)
	require.Equal(t, policyReasonNone, reason)

	require.True(t, candidateAllowedByPolicy(rankAMD64v2, rankAMD64v4, rankAMD64v3, false))
	require.False(t, candidateAllowedByPolicy(rankAMD64v4, rankAMD64v4, rankAMD64v3, false))
	require.False(t, candidateAllowedByPolicy(rankAMD64v2, rankAMD64v4, rankAMD64v3, true))
	require.False(t, candidateAllowedByPolicy(-1, rankAMD64v4, rankAMD64v4, false))

	require.True(t, amd64RankSatisfied(rankAMD64v2, rankAMD64v3))
	require.False(t, amd64RankSatisfied(rankAMD64v4, rankAMD64v3))
	require.False(t, amd64RankSatisfied(0, rankAMD64v3))
}
