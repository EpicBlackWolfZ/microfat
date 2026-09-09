package system

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const missingExecutable = "/missing"

func TestResourceOptions(t *testing.T) {
	t.Parallel()
	for name, options := range map[string]Options{
		"zero period": {}, "negative memory": {CPUPeriodUS: DefaultPeriod, MemoryBytes: -1},
		"version":       {CPUPeriodUS: DefaultPeriod, Version: "v3"},
		"negative CPU":  {CPUPeriodUS: DefaultPeriod, Affinity: []int{-1}},
		"duplicate CPU": {CPUPeriodUS: DefaultPeriod, Affinity: []int{1, 1}},
		"relative path": {CPUPeriodUS: DefaultPeriod, CgroupRoot: "relative"},
	} {
		t.Run(name, func(t *testing.T) { _, err := Prepare(options); require.Error(t, err) })
	}
	options := Options{CPUPeriodUS: DefaultPeriod}
	sandbox, err := Prepare(options)
	require.NoError(t, err)
	assert.NotEmpty(t, Uncontrolled(sandbox.Controls))
	require.NoError(t, sandbox.Close())
	options.CgroupRoot = t.TempDir()
	sandbox, err = Prepare(options)
	require.NoError(t, err)
	assert.Contains(t, Uncontrolled(sandbox.Controls)[1], "uncontrolled cgroup")
	require.NoError(t, sandbox.Close())
	assert.Equal(t, "1,2", CPUList([]int{1, 2}))
}

func fixtureGroup(t *testing.T) func(string) (string, error) {
	t.Helper()
	return func(root string) (string, error) {
		dir, err := os.MkdirTemp(root, "group-")
		require.NoError(t, err)
		for _, name := range []string{"cpu.max", "memory.max", "cpu.cfs_quota_us", "cpu.cfs_period_us", "memory.limit_in_bytes"} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, controlMode))
		}
		return dir, nil
	}
}

func TestCgroupControllerContracts(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			s := &Sandbox{Options: Options{CPUPeriodUS: DefaultPeriod, CPUQuotaUS: DefaultPeriod, MemoryBytes: kibibyte,
				CgroupRoot: t.TempDir(), MemoryRoot: t.TempDir(), Version: version}, mkdirGroup: fixtureGroup(t)}
			require.NoError(t, s.prepareCgroups())
			assert.NotEmpty(t, s.Procs)
			assert.Empty(t, Uncontrolled(s.Controls))
			// Real cgroup files are kernel-owned; fixture files need removal before rmdir.
			require.Error(t, s.Close())
			for _, dir := range s.Paths {
				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				for _, entry := range entries {
					require.NoError(t, os.Remove(filepath.Join(dir, entry.Name())))
				}
			}
			require.NoError(t, s.Close())
		})
	}
	s := &Sandbox{Options: Options{CPUPeriodUS: DefaultPeriod, CgroupRoot: t.TempDir()}, mkdirGroup: fixtureGroup(t)}
	require.NoError(t, s.prepareCgroups())
	s.Options.Version = "v1"
	require.ErrorContains(t, s.prepareCgroups(), "memory controller")
	s.Options.MemoryRoot = t.TempDir()
	s.mkdirGroup = func(string) (string, error) { return "", errors.New("permission denied") }
	require.Error(t, s.prepareCgroups())
	s.Paths = []string{filepath.Join(t.TempDir(), "already removed")}
	require.NoError(t, s.Close())
}

func TestBootstrapAndAffinity(t *testing.T) {
	t.Parallel()
	s := &Sandbox{Options: Options{Affinity: []int{1}}, Procs: []string{"/delegated/cgroup.procs"}}
	spec := s.Wrap("/helper", process.Spec{Path: "/target", Args: []string{"arg"}, Env: []string{"X=1"}, Dir: "/work"})
	assert.Equal(t, "/helper", spec.Path)
	child, err := ParseChild([]byte(spec.Args[2]))
	require.NoError(t, err)
	assert.Equal(t, "/target", child.Path)
	assert.Equal(t, []string{"X=1"}, child.Env)
	for _, data := range []string{"{", `{"path":"relative"}`, `{"path":"/target","procs":["../cgroup.procs"]}`,
		`{"path":"/target","affinity":[-1]}`} {
		_, err := ParseChild([]byte(data))
		require.Error(t, err)
	}
	_, err = ParseChild(make([]byte, process.MaxOutput+1))
	require.Error(t, err)
	if runtime.GOOS == "linux" {
		cpus, err := SupportedAffinity(nil)
		require.NoError(t, err)
		require.NotEmpty(t, cpus)
		sandbox, err := Prepare(Options{CPUPeriodUS: DefaultPeriod, Affinity: cpus[:1]})
		require.NoError(t, err)
		assert.Equal(t, "applied", sandbox.Controls[0].State)
	}
}

func TestTelemetryAndAvailability(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "1")
	require.NoError(t, os.Mkdir(dir, defaultDirMode))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status"), []byte("VmRSS:\t2 kB\nVmSize: 4 kB\nVmHWM: 3 kB\n"+
		"voluntary_ctxt_switches: 1\nnonvoluntary_ctxt_switches: 0\n"), controlMode))
	metrics := ReadProcess(root, 1, "startup")
	assert.Equal(t, float64(2*kibibyte), *metrics["rss_bytes"].Value)
	assert.Nil(t, ReadProcess(root, 2, "startup")["rss_bytes"].Value)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status"), []byte("VmRSS: invalid"), controlMode))
	assert.Nil(t, ReadProcess(root, 1, "startup")["rss_bytes"].Value)
	for _, value := range []string{"", "invalid", "-1", "NaN", "Inf"} {
		_, err := firstNumber(value)
		require.Error(t, err)
	}
	cgroup := t.TempDir()
	for name, data := range map[string]string{"memory.current": "2048", "memory.peak": "bad",
		"cpu.stat":    "usage_usec 10\nnr_throttled 1\nthrottled_time 2\ninvalid text\n",
		"memory.stat": "shmem 100\npgfault 4\n", "memory.events": "oom 0\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(cgroup, name), []byte(data), controlMode))
	}
	s := &Sandbox{Paths: []string{cgroup}}
	values := s.Read("steady_state")
	assert.Equal(t, "count", values["memory.stat/pgfault"].Unit)
	assert.Equal(t, "bytes", values["memory.stat/shmem"].Unit)
	assert.Equal(t, "us", values["cpu.stat/usage_usec"].Unit)
	assert.Nil(t, values["memory.peak"].Value)
	assert.Nil(t, (&Sandbox{}).Read("startup")["cgroup_memory_bytes"].Value)
	a := schema.Measured(1, "count", "measurement", "fixture")
	b := schema.Measured(2, "count", "measurement", "fixture")
	assert.Equal(t, 1.0, *Delta(a, b).Value)
	assert.Nil(t, Delta(b, a).Value)
	assert.Equal(t, "unavailable", ReadHost(t.TempDir())["frequency"])
	freq := filepath.Join(root, "devices/system/cpu/cpu0/cpufreq")
	require.NoError(t, os.MkdirAll(freq, defaultDirMode))
	require.NoError(t, os.WriteFile(filepath.Join(freq, "scaling_governor"), []byte("performance"), controlMode))
	assert.Contains(t, ReadHost(root), filepath.Join(freq, "scaling_governor"))
	assert.Empty(t, Usage(nil))
}

func TestRealCgroupIntegration(t *testing.T) {
	root := os.Getenv("MICROFAT_BENCH_CGROUP_ROOT")
	if root == "" {
		t.Skip("set a delegated MICROFAT_BENCH_CGROUP_ROOT for real kernel integration")
	}
	s, err := Prepare(Options{CPUPeriodUS: DefaultPeriod, CPUQuotaUS: DefaultPeriod,
		CgroupRoot: root, MemoryRoot: os.Getenv("MICROFAT_BENCH_MEMORY_ROOT"), Version: os.Getenv("MICROFAT_BENCH_CGROUP_VERSION")})
	require.NoError(t, err)
	defer s.Close()
	require.NotEmpty(t, s.Procs)
	for _, control := range s.Controls {
		if control.Name != "affinity" {
			assert.Equal(t, "applied", control.State)
		}
	}
}

func TestExecChildHelper(t *testing.T) {
	data := os.Getenv("MICROFAT_TEST_BOOTSTRAP")
	if data == "" {
		return
	}
	cfg, err := ParseChild([]byte(data))
	if err == nil {
		err = ExecChild(cfg)
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestBootstrapExecAndUsage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux bootstrap")
	}
	path, err := os.Executable()
	require.NoError(t, err)
	cpus, err := SupportedAffinity(nil)
	require.NoError(t, err)
	for _, cfg := range []ChildConfig{{Path: "/bin/true", Affinity: cpus[:1]}, {Path: missingExecutable},
		{Path: "/bin/true", Procs: []string{"/missing/cgroup.procs"}}} {
		data, err := json.Marshal(cfg)
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		child, err := process.Start(ctx, process.Spec{Path: path, Args: []string{"-test.run=^TestExecChildHelper$"},
			Env: append(os.Environ(), "MICROFAT_TEST_BOOTSTRAP="+string(data))})
		require.NoError(t, err)
		err = child.Wait()
		if cfg.Path == "/bin/true" && len(cfg.Procs) == 0 {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
		assert.NotEmpty(t, Usage(child.State()))
		cancel()
	}
	assert.NotEmpty(t, ReadProcess("/proc", os.Getpid(), "test"))
	assert.NotEmpty(t, strconv.Itoa(os.Getpid()))
}
