package env

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectorMembershipWhitespace(t *testing.T) {
	t.Parallel()
	for _, version := range []string{cgroupVersionV1, cgroupVersionV2} {
		for _, suffix := range []string{" ", "\t", "\r"} {
			for _, sibling := range []bool{false, true} {
				t.Run(version+suffix+map[bool]string{false: " absent", true: " sibling"}[sibling], func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					base, filename, proc := root, cgroupV2MemoryMaxFile, "0::/app"+suffix+"\n"
					if version == cgroupVersionV1 {
						base = filepath.Join(root, "memory")
						filename, proc = cgroupV1MemoryLimitFile, "3:memory:/app"+suffix+"\n"
					} else {
						writeCgroupBoundaryFixture(t, root, cgroupV2ControllersFile, "memory cpu")
					}
					writeCgroupBoundaryFixture(t, base, filename, "1073741824")
					writeCgroupBoundaryFixture(t, base, filepath.Join("app"+suffix, filename), "134217728")
					if sibling {
						writeCgroupBoundaryFixture(t, base, filepath.Join("app", filename), "1073741824")
					}
					procFile := filepath.Join(t.TempDir(), "cgroup")
					require.NoError(t, os.WriteFile(procFile, []byte(proc), 0o600))
					detector := NewCustomDetector(root, procFile, t.TempDir(), t.TempDir())
					snapshot, err := detector.Detect()
					require.NoError(t, err)
					assert.Equal(t, version, snapshot.Host.Cgroup.Version)
					assert.Equal(t, schema.NewFiniteLimit(int64(134217728)), snapshot.Host.Cgroup.MemoryMaxBytes)
				})
			}
		}
	}
}

func TestDetectorFiniteZeroMemory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeCgroupBoundaryFixture(t, root, cgroupV2MemoryMaxFile, "0")
	writeCgroupBoundaryFixture(t, root, cgroupV2CPUMaxFile, "200000 100000")
	procFile := filepath.Join(t.TempDir(), "cgroup")
	require.NoError(t, os.WriteFile(procFile, []byte("0::/\n"), 0o600))
	detector := NewCustomDetector(root, procFile, t.TempDir(), t.TempDir())
	snapshot, err := detector.Detect()
	require.NoError(t, err)
	assert.Equal(t, schema.NewFiniteLimit(int64(0)), snapshot.Host.Cgroup.MemoryMaxBytes)
	assert.Equal(t, schema.NewFiniteLimit(testQuota200ms), snapshot.Host.Cgroup.CPUQuotaUs)
}

func writeCgroupBoundaryFixture(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
}
