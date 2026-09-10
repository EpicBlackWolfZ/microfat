//go:build linux

package system

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const kernelAbove = "above"
const kernelWait = "wait"
const kernelLimit = 64 * 1024 * 1024
const kernelTimeout = 30 * time.Second

// TestKernelWorkerHelper is exec'd inside the test-owned cgroup, never in the runner's own group.
func TestKernelWorkerHelper(t *testing.T) {
	mode := os.Getenv("MICROFAT_KERNEL_WORKER")
	if mode == "" {
		return
	}
	if data := os.Getenv("MICROFAT_KERNEL_BOOTSTRAP"); data != "" {
		cfg, err := ParseChild([]byte(data))
		require.NoError(t, err)
		require.NoError(t, ExecChild(cfg))
		return
	}
	fmt.Println("ready")
	switch mode {
	case "cpu":
		end := time.Now().Add(4 * time.Second)
		var count uint64
		for time.Now().Before(end) {
			count++
		}
		fmt.Println(count)
	case "below", kernelAbove:
		size := kernelLimit / 4
		if mode == kernelAbove {
			size = kernelLimit * 2
		}
		data, err := unix.Mmap(-1, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON)
		require.NoError(t, err)
		for i := range data {
			data[i] = byte(i)
		}
		fmt.Println("allocated", len(data))
		require.NoError(t, unix.Munmap(data))
	case kernelWait:
		time.Sleep(kernelTimeout)
	default:
		t.Fatal("unknown kernel worker")
	}
	os.Exit(0)
}

func kernelSandbox(t *testing.T) *Sandbox {
	t.Helper()
	root := os.Getenv("MICROFAT_BENCH_CGROUP_ROOT")
	if root == "" {
		if os.Getenv("MICROFAT_BENCH_REQUIRE_CONTROLS") == "1" {
			t.Fatal("required delegated root missing")
		}
		t.Skip("kernel integration requires a delegated cgroup root")
	}
	cpus, err := SupportedAffinity(nil)
	require.NoError(t, err)
	require.NotEmpty(t, cpus)
	s, err := Prepare(Options{CPUPeriodUS: DefaultPeriod, CPUQuotaUS: DefaultPeriod / 4, MemoryBytes: kernelLimit,
		Affinity: cpus[:1], CgroupRoot: root, MemoryRoot: os.Getenv("MICROFAT_BENCH_MEMORY_ROOT"),
		Version: os.Getenv("MICROFAT_BENCH_CGROUP_VERSION")})
	require.NoError(t, err)
	require.NotEmpty(t, s.Procs)
	require.Empty(t, Uncontrolled(s.Controls))
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	for _, path := range s.Paths {
		file := filepath.Join(path, "memory.swap.max")
		if _, err := os.Stat(file); err == nil {
			require.NoError(t, os.WriteFile(file, []byte("0"), controlMode))
		}
	}
	t.Logf("effective controls: %+v", s.Controls)
	return s
}

func kernelChild(t *testing.T, ctx context.Context, s *Sandbox, mode string) *process.Child {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	args := []string{"-test.run=^TestKernelWorkerHelper$"}
	env := append(os.Environ(), "MICROFAT_KERNEL_WORKER="+mode, "MICROFAT_KERNEL_BOOTSTRAP=")
	data, err := json.Marshal(ChildConfig{Path: executable, Args: args, Env: env, Affinity: s.Options.Affinity, Procs: s.Procs})
	require.NoError(t, err)
	child, err := process.Start(ctx, process.Spec{Path: executable, Args: args,
		Env: append(os.Environ(), "MICROFAT_KERNEL_WORKER="+mode, "MICROFAT_KERNEL_BOOTSTRAP="+string(data))})
	require.NoError(t, err)
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = child.Stop(stop)
	})
	line, _, err := child.FirstLine(ctx)
	require.NoError(t, err, string(child.Stderr()))
	require.Equal(t, "ready", string(line))
	return child
}

func TestKernelControls(t *testing.T) {
	for _, mode := range []string{"cpu", "below", kernelAbove, kernelWait} {
		t.Run(mode, func(t *testing.T) {
			s := kernelSandbox(t)
			ctx, cancel := context.WithTimeout(context.Background(), kernelTimeout)
			defer cancel()
			before := s.Read("before")
			child := kernelChild(t, ctx, s, mode)
			if mode == "cpu" || mode == kernelWait {
				for _, file := range s.Procs {
					data, err := os.ReadFile(file)
					require.NoError(t, err)
					require.True(t, containsPID(string(data), child.PID()))
				}
				var effective unix.CPUSet
				require.NoError(t, unix.SchedGetaffinity(child.PID(), &effective))
				require.Equal(t, 1, effective.Count())
				require.True(t, effective.IsSet(s.Options.Affinity[0]))
			}
			if mode == kernelWait {
				cancel()
			}
			err := child.Wait()
			if mode == kernelAbove || mode == kernelWait {
				require.Error(t, err)
			} else {
				require.NoError(t, err, string(child.Stderr()))
			}
			after := s.Read("after")
			t.Logf("before=%+v after=%+v usage=%+v", before, after, Usage(child.State()))
			if mode == "cpu" {
				metric := after["cpu.stat/nr_throttled"]
				require.NotNil(t, metric.Value)
				assert.Positive(t, *metric.Value)
				assert.Positive(t, *Usage(child.State())["user_cpu_seconds"].Value)
			}
			if mode == kernelAbove {
				key := "memory.events/oom_kill"
				if s.Options.Version == "v1" {
					key = "memory.failcnt"
				}
				metric := after[key]
				require.NotNil(t, metric.Value)
				assert.Positive(t, *metric.Value)
			} else if mode == "below" {
				assert.Contains(t, string(child.Stdout()), "allocated")
			}
			for _, file := range s.Procs {
				data, err := os.ReadFile(file)
				require.NoError(t, err)
				assert.Empty(t, strings.TrimSpace(string(data)))
			}
			require.NoError(t, s.Close())
		})
	}
}

func TestEffectiveKernelLimits(t *testing.T) {
	t.Parallel()
	const page = 4096
	for _, test := range []struct {
		name, want, got string
		valid           bool
	}{
		{"cpu.max", "1000 100000", "1000 100000", true}, {"cpu.max", "1000 100000", "max 100000", false},
		{"memory.max", "4097", "4096", true}, {"memory.max", "4097", "8192", false},
		{"memory.limit_in_bytes", "bad", "4096", false}, {"memory.max", "4096", "bad", false},
	} {
		assert.Equal(t, test.valid, effectiveLimit(test.name, test.want, test.got, page))
	}
	assert.False(t, effectiveLimit("memory.max", "1", "0", 0))
	require.Error(t, CheckCgroupRoot(t.TempDir(), "v2"))
	require.Error(t, CheckCgroupRoot("/missing", "v1"))
	assert.True(t, containsPID("1\n"+strconv.Itoa(os.Getpid()), os.Getpid()))
	assert.False(t, containsPID("0", os.Getpid()))
}
