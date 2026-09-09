package runtimeinit

import (
	"bytes"
	"errors"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
)

func TestMemoryMetadataIsNotTrusted(t *testing.T) {
	old := retainedExecutableMemoryFunc
	t.Cleanup(func() { retainedExecutableMemoryFunc = old })
	const ceiling = int64(1024 * 1024 * 1024)
	const retained = int64(128 * 1024 * 1024)
	limits := &cgroup.Limits{CgroupVersion: cgroup.VersionV2, MemoryLimitBytes: ceiling}
	for _, tc := range []struct {
		name     string
		env      map[string]string
		probeErr error
		expected int64
	}{
		{"forged size ignored", map[string]string{format.EnvSelectedSize: "999999999999999999", format.EnvExecMode: "memfd"}, nil, retained},
		{"explicit value wins", map[string]string{"GOMEMLIMIT": "123456B"}, nil, -1},
		{"unknown executable storage", nil, errors.New("probe unavailable"), -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retainedExecutableMemoryFunc = func() (int64, error) { return retained, tc.probeErr }
			withIsolatedEnv(t, tc.env, limits, nil, func(mem *int64, _ *int, _ *int, _ *bytes.Buffer) {
				AutoTune()
				if tc.expected < 0 {
					require.EqualValues(t, -1, *mem)
					return
				}
				expected, ok := cgroup.CalculateGOMEMLIMIT(ceiling-retained, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes)
				require.True(t, ok)
				require.Equal(t, expected, *mem)
				AutoTune()
				require.Equal(t, expected, *mem)
			})
		})
	}
}
