package builder

import (
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssemblePackOptionsCreationEpoch(t *testing.T) {
	t.Parallel()
	m := &Manifest{}
	opts := assemblePackOptions(m, "stub", "out", "app", nil, BuildOptions{})
	assert.Nil(t, opts.CreatedUnix)
	for _, epoch := range []int64{math.MinInt64, -1, 0, math.MaxInt64} {
		t.Run(strconv.FormatInt(epoch, 10), func(t *testing.T) {
			t.Parallel()
			opts := assemblePackOptions(m, "stub", "out", "app", nil, BuildOptions{CreatedUnix: &epoch})
			require.NotNil(t, opts.CreatedUnix)
			assert.Equal(t, epoch, *opts.CreatedUnix)
		})
	}
}

func TestAssembleTargetEnvPreservesExportedSourceDateEpoch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		ambient []string
		want    string
	}{
		{"exported", []string{"SOURCE_DATE_EPOCH=0001"}, "SOURCE_DATE_EPOCH=0001"},
		{"exported empty", []string{"SOURCE_DATE_EPOCH="}, "SOURCE_DATE_EPOCH="},
		{"deduplicated", []string{"SOURCE_DATE_EPOCH=1", "SOURCE_DATE_EPOCH=-2"}, "SOURCE_DATE_EPOCH=-2"},
		{"not exported", nil, "SOURCE_DATE_EPOCH=variant"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &Manifest{TargetOS: testOSLinux, TargetArch: testArchAMD64, Env: map[string]string{sourceDateEpochEnv: "root"}}
			v := VariantConfig{Level: "v1", Env: map[string]string{sourceDateEpochEnv: "variant"}}
			env := assembleTargetEnv(tc.ambient, m, v)
			assert.Contains(t, env, tc.want)
			assert.Len(t, env, 4, "one creation epoch plus three authoritative target variables")
		})
	}
}
