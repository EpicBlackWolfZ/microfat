package env

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNestedCPUQuotaPeriods(t *testing.T) {
	t.Parallel()
	for _, version := range []string{cgroupVersionV1, cgroupVersionV2} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name                      string
				parentQuota, parentPeriod int64
				childQuota, childPeriod   int64
				wantQuota, wantPeriod     int64
				finite                    bool
			}{
				{"parent tighter despite larger quota", 100000, 100000, 50000, 10000, 100000, 100000, true},
				{"child tighter despite larger quota", 50000, 10000, 100000, 100000, 100000, 100000, true},
				{"equal ratio retains leaf pair", 100000, 100000, 50000, 50000, 50000, 50000, true},
				{"unlimited parent", -1, 100000, 50000, 10000, 50000, 10000, true},
				{"unlimited child", 100000, 100000, -1, 10000, 100000, 100000, true},
				{"all unlimited retains leaf period", -1, 100000, -1, 10000, 0, 10000, false},
				{"large ratios retain integer precision", math.MaxInt64 - 1, math.MaxInt64,
					math.MaxInt64, math.MaxInt64, math.MaxInt64 - 1, math.MaxInt64, true},
				{"cross products exceed uint64", math.MaxInt64, math.MaxInt64 / 2,
					math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					child := filepath.Join(root, "child")
					require.NoError(t, os.Mkdir(child, 0o700))
					writeCPUQuotaFixture(t, version, root, tc.parentQuota, tc.parentPeriod)
					writeCPUQuotaFixture(t, version, child, tc.childQuota, tc.childPeriod)
					quota, period, finite, err := traverseCPUQuotaFixture(version, root, child)
					require.NoError(t, err)
					assert.Equal(t, tc.wantQuota, quota)
					require.NotNil(t, period)
					assert.Equal(t, tc.wantPeriod, *period)
					assert.Equal(t, tc.finite, finite)
				})
			}
		})
	}
}

func TestCPUQuotaInvalidPeriod(t *testing.T) {
	t.Parallel()
	for _, version := range []string{cgroupVersionV1, cgroupVersionV2} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			for _, invalid := range []string{"missing", "bad", "0", "-1", "9223372036854775808", "directory"} {
				t.Run(invalid, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					child := filepath.Join(root, "child")
					require.NoError(t, os.Mkdir(child, 0o700))
					writeCPUQuotaFixture(t, version, root, 100000, 100000)
					writeCPUQuotaFixture(t, version, child, 50000, 10000)
					periodPath := filepath.Join(child, cgroupV1CPUPeriodFile)
					value := invalid
					if version == cgroupVersionV2 {
						periodPath = filepath.Join(child, cgroupV2CPUMaxFile)
						value = "50000 " + invalid
					}
					require.NoError(t, os.Remove(periodPath))
					switch invalid {
					case "directory":
						require.NoError(t, os.Mkdir(periodPath, 0o700))
					case "missing":
						if version == cgroupVersionV2 {
							require.NoError(t, os.WriteFile(periodPath, []byte("50000"), 0o600))
						}
					default:
						require.NoError(t, os.WriteFile(periodPath, []byte(value), 0o600))
					}
					quota, period, finite, err := traverseCPUQuotaFixture(version, root, child)
					require.Error(t, err)
					assert.Zero(t, quota)
					assert.Nil(t, period)
					assert.False(t, finite)
				})
			}
		})
	}
}

func writeCPUQuotaFixture(t *testing.T, version, dir string, quota, period int64) {
	t.Helper()
	quotaText := strconv.FormatInt(quota, 10)
	periodText := strconv.FormatInt(period, 10)
	if version == cgroupVersionV2 {
		if quota == -1 {
			quotaText = cgroupMaxKeyword
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, cgroupV2CPUMaxFile), []byte(quotaText+" "+periodText), 0o600))
		return
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, cgroupV1CPUQuotaFile), []byte(quotaText), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, cgroupV1CPUPeriodFile), []byte(periodText), 0o600))
}

func traverseCPUQuotaFixture(version, root, child string) (int64, *int64, bool, error) {
	if version == cgroupVersionV2 {
		return traverseCgroupV2CPUMax(child, root)
	}
	return traverseCgroupV1CPU(root, child)
}
