package cgroup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadLimitsCustomCPUOnlyV1(t *testing.T) {
	t.Parallel()
	const (
		ancestorQuota = 2.0
		currentQuota  = 3.5
		ancestorCPUs  = 2
		currentCPUs   = 3
	)
	for _, layout := range []string{"cpu", "cpu,cpuacct", "."} {
		t.Run(layout, func(t *testing.T) {
			t.Parallel()
			cases := []struct {
				name         string
				rootQuota    string
				currentQuota string
				wantQuota    float64
				wantCPUs     int
			}{
				{name: "current quota", rootQuota: "-1", currentQuota: "350000", wantQuota: currentQuota, wantCPUs: currentCPUs},
				{name: "ancestor quota", rootQuota: "200000", currentQuota: "-1", wantQuota: ancestorQuota, wantCPUs: ancestorCPUs},
				{name: "tightest quota", rootQuota: "200000", currentQuota: "350000", wantQuota: ancestorQuota, wantCPUs: ancestorCPUs},
				{name: "unlimited quota", rootQuota: "-1", currentQuota: "-1"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					base := filepath.Join(root, layout)
					writeV1DetectionFiles(t, base, map[string]string{
						"cpu.cfs_quota_us":               tc.rootQuota,
						"cpu.cfs_period_us":              "100000",
						"parent/child/cpu.cfs_quota_us":  tc.currentQuota,
						"parent/child/cpu.cfs_period_us": "100000",
					})
					proc := filepath.Join(t.TempDir(), "cgroup")
					require.NoError(t, os.WriteFile(proc, []byte("4:cpu,cpuacct:/parent/child\n"), 0o600))

					limits, err := ReadLimitsCustom(root, proc)
					require.NoError(t, err)
					assert.Equal(t, Limits{CgroupVersion: VersionV1, CPUQuota: tc.wantQuota, CPUs: tc.wantCPUs}, limits)
				})
			}
		})
	}
}

func TestReadLimitsCustomV1ControllerDetection(t *testing.T) {
	t.Parallel()
	const (
		quotaCPUs     = 2
		cpuRootProc   = "4:cpu:/\n"
		cpuPeriodPath = "cpu/cpu.cfs_period_us"
		cpuQuotaPath  = "cpu/cpu.cfs_quota_us"
	)
	wantCPU := Limits{CgroupVersion: VersionV1, CPUQuota: quotaCPUs, CPUs: quotaCPUs}
	wantMemory := Limits{CgroupVersion: VersionV1, MemoryLimitBytes: testBytes1GB, EffectiveMemoryLimitBytes: testBytes1GB}
	wantCombined := wantMemory
	wantCombined.CPUQuota = quotaCPUs
	wantCombined.CPUs = quotaCPUs

	cases := []struct {
		name    string
		proc    string
		files   map[string]string
		want    Limits
		wantErr error
	}{
		{
			name: "cpu controller root", proc: cpuRootProc, want: wantCPU,
			files: map[string]string{cpuQuotaPath: "200000", cpuPeriodPath: "100000"},
		},
		{
			name: "combined cpu controller root", proc: "4:cpu,cpuacct:/\n", want: wantCPU,
			files: map[string]string{"cpu,cpuacct/cpu.cfs_quota_us": "200000", "cpu,cpuacct/cpu.cfs_period_us": "100000"},
		},
		{
			name: "direct cpu controller root", proc: cpuRootProc, want: wantCPU,
			files: map[string]string{"cpu.cfs_quota_us": "200000", "cpu.cfs_period_us": "100000"},
		},
		{
			name: "memory only", proc: "5:memory:/\n", want: wantMemory,
			files: map[string]string{"memory/memory.limit_in_bytes": "1073741824"},
		},
		{
			name: "direct memory only", proc: "5:memory:/\n", want: wantMemory,
			files: map[string]string{"memory.limit_in_bytes": "1073741824"},
		},
		{
			name: "combined memory and cpu", proc: "5:memory:/\n4:cpu:/\n", want: wantCombined,
			files: map[string]string{
				"memory/memory.limit_in_bytes": "1073741824",
				cpuQuotaPath:                   "200000", cpuPeriodPath: "100000",
			},
		},
		{
			name: "unlimited memory", proc: "5:memory:/\n4:cpu:/\n", want: wantCPU,
			files: map[string]string{
				"memory/memory.limit_in_bytes": "-1",
				cpuQuotaPath:                   "200000", cpuPeriodPath: "100000",
			},
		},
		{
			name: "missing memory data", proc: "5:memory:/\n4:cpu:/\n", want: wantCPU,
			files: map[string]string{cpuQuotaPath: "200000", cpuPeriodPath: "100000"},
		},
		{
			name: "unresolved cpu controller", proc: "3:cpuset:/\n", want: Limits{CgroupVersion: VersionUnknown},
			files: map[string]string{cpuQuotaPath: "200000", cpuPeriodPath: "100000"},
		},
		{
			name: "missing current hierarchy", proc: "4:cpu:/missing\n", wantErr: ErrCgroupHierarchyNotFound,
			files: map[string]string{cpuQuotaPath: "200000", cpuPeriodPath: "100000"},
		},
		{
			name: "malformed quota", proc: cpuRootProc, wantErr: ErrCgroupLimitCorrupted,
			files: map[string]string{cpuQuotaPath: "bad-quota", cpuPeriodPath: "100000"},
		},
		{
			name: "malformed period", proc: cpuRootProc, wantErr: ErrCgroupLimitCorrupted,
			files: map[string]string{cpuQuotaPath: "200000", cpuPeriodPath: "0"},
		},
		{
			name: "missing period", proc: cpuRootProc, want: Limits{CgroupVersion: VersionV1},
			files: map[string]string{cpuQuotaPath: "200000"},
		},
		{
			name: "v2 detection retains precedence", proc: "0::/\n4:cpu:/\n", want: Limits{CgroupVersion: VersionV2},
			files: map[string]string{
				"cgroup.controllers": "memory cpu", cpuQuotaPath: "200000", cpuPeriodPath: "100000",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeV1DetectionFiles(t, root, tc.files)
			proc := filepath.Join(t.TempDir(), "cgroup")
			require.NoError(t, os.WriteFile(proc, []byte(tc.proc), 0o600))
			limits, err := ReadLimitsCustom(root, proc)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Equal(t, Limits{CgroupVersion: VersionUnknown}, limits)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, limits)
		})
	}
}

func writeV1DetectionFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	}
}
