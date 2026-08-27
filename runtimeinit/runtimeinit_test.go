package runtimeinit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	testFilePerm               = 0o644
	testProfileLatencyCritical = "latency_critical"
)

// withIsolatedEnv runs fn with overridden global test hooks and restores them afterwards.
func withIsolatedEnv(
	t *testing.T,
	mockEnv map[string]string,
	mockLimits *cgroup.Limits,
	mockLimitsErr error,
	fn func(
		memLimitSet *int64,
		maxProcsSet *int,
		gogcSet *int,
		stderrBuf *bytes.Buffer,
	),
) {
	t.Helper()

	origSetMem := setMemoryLimitFunc
	origSetCPU := setMaxProcsFunc
	origSetGC := setGCPercentFunc
	origReadLimits := readLimitsFunc
	origReadLimitsFrom := readLimitsFromFunc
	origGetenv := getenvFunc
	origStderr := stderrWriter

	defer func() {
		setMemoryLimitFunc = origSetMem
		setMaxProcsFunc = origSetCPU
		setGCPercentFunc = origSetGC
		readLimitsFunc = origReadLimits
		readLimitsFromFunc = origReadLimitsFrom
		getenvFunc = origGetenv
		stderrWriter = origStderr
	}()

	var recordedMemLimit int64 = -1
	var recordedMaxProcs int = -1
	var recordedGOGC int = -999
	stderrBuf := &bytes.Buffer{}

	setMemoryLimitFunc = func(limit int64) int64 {
		recordedMemLimit = limit
		return limit
	}
	setMaxProcsFunc = func(n int) int {
		recordedMaxProcs = n
		return n
	}
	setGCPercentFunc = func(percent int) int {
		recordedGOGC = percent
		return percent
	}
	getenvFunc = func(key string) string {
		if val, ok := mockEnv[key]; ok {
			return val
		}
		return ""
	}
	stderrWriter = stderrBuf

	if mockLimits != nil || mockLimitsErr != nil {
		readLimitsFunc = func() (cgroup.Limits, error) {
			if mockLimitsErr != nil {
				return cgroup.Limits{}, mockLimitsErr
			}
			return *mockLimits, nil
		}
		readLimitsFromFunc = func(root string) (cgroup.Limits, error) {
			if mockLimitsErr != nil {
				return cgroup.Limits{}, mockLimitsErr
			}
			return *mockLimits, nil
		}
	}

	fn(&recordedMemLimit, &recordedMaxProcs, &recordedGOGC, stderrBuf)
}

func TestAutoTune_Disabled(t *testing.T) {
	testCases := []struct {
		name   string
		envVal string
	}{
		{name: "DisabledWithZero", envVal: "0"},
		{name: "DisabledWithFalse", envVal: "false"},
		{name: "DisabledWithFalseUppercase", envVal: "FALSE"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mockEnv := map[string]string{
				format.EnvAutotune: tc.envVal,
			}
			withIsolatedEnv(t, mockEnv, nil, nil, func(memLimit *int64, maxProcs *int, gogc *int, _ *bytes.Buffer) {
				res := AutoTune()
				if *memLimit != -1 {
					t.Errorf("expected memLimit to be unset (-1), got %d", *memLimit)
				}
				if *maxProcs != -1 {
					t.Errorf("expected maxProcs to be unset (-1), got %d", *maxProcs)
				}
				if *gogc != -999 {
					t.Errorf("expected gogc to be unset (-999), got %d", *gogc)
				}
				if res.MemLimitApplied {
					t.Errorf("expected MemLimitApplied to be false")
				}
				if res.MaxProcsApplied {
					t.Errorf("expected MaxProcsApplied to be false")
				}
				if res.GOGCApplied {
					t.Errorf("expected GOGCApplied to be false")
				}
				if !strings.Contains(res.SkippedReason, "auto-tuning disabled") {
					t.Errorf("unexpected SkippedReason: %q", res.SkippedReason)
				}
			})
		})
	}
}

func TestAutoTune_CgroupUnavailableOrUnknown(t *testing.T) {
	t.Run("UnknownVersion", func(t *testing.T) {
		mockLimits := cgroup.Limits{CgroupVersion: cgroup.VersionUnknown}
		withIsolatedEnv(t, nil, &mockLimits, nil, func(memLimit *int64, maxProcs *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune()
			if *memLimit != -1 || *maxProcs != -1 || *gogc != -999 {
				t.Errorf("expected no limits applied")
			}
			if res.MemLimitApplied || res.MaxProcsApplied || res.GOGCApplied {
				t.Errorf("expected limits applied flags to be false")
			}
			if !strings.Contains(res.SkippedReason, "cgroup resource limits not detected") {
				t.Errorf("unexpected SkippedReason: %q", res.SkippedReason)
			}
		})
	})

	t.Run("InspectionError", func(t *testing.T) {
		inspectErr := errors.New("permission denied")
		withIsolatedEnv(t, nil, nil, inspectErr, func(memLimit *int64, maxProcs *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune()
			if *memLimit != -1 || *maxProcs != -1 || *gogc != -999 {
				t.Errorf("expected no limits applied")
			}
			if !strings.Contains(res.SkippedReason, "cgroup inspection failed: permission denied") {
				t.Errorf("unexpected SkippedReason: %q", res.SkippedReason)
			}
		})
	})
}

func TestAutoTune_CgroupV2_Success(t *testing.T) {
	const mem1GB int64 = 1024 * 1024 * 1024
	const quota4CPUs float64 = 4.0
	const expectedCPUs int = 4

	mockLimits := cgroup.Limits{
		CgroupVersion:    cgroup.VersionV2,
		MemoryLimitBytes: mem1GB,
		CPUQuota:         quota4CPUs,
		CPUs:             expectedCPUs,
	}

	withIsolatedEnv(t, nil, &mockLimits, nil, func(memLimit *int64, maxProcs *int, gogc *int, _ *bytes.Buffer) {
		res := AutoTune()

		expectedMemLimit, ok := cgroup.CalculateGOMEMLIMIT(mem1GB, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes)
		if !ok {
			t.Fatalf("CalculateGOMEMLIMIT failed unexpectedly")
		}

		if !res.MemLimitApplied || !res.MaxProcsApplied {
			t.Errorf("expected limits to be applied: mem=%t cpu=%t", res.MemLimitApplied, res.MaxProcsApplied)
		}
		if res.GOMEMLIMIT != expectedMemLimit || *memLimit != expectedMemLimit {
			t.Errorf("expected GOMEMLIMIT %d, got res=%d set=%d", expectedMemLimit, res.GOMEMLIMIT, *memLimit)
		}
		if res.GOMAXPROCS != expectedCPUs || *maxProcs != expectedCPUs {
			t.Errorf("expected GOMAXPROCS %d, got res=%d set=%d", expectedCPUs, res.GOMAXPROCS, *maxProcs)
		}
		if res.CgroupVersion != cgroup.VersionV2 {
			t.Errorf("expected CgroupVersion 2, got %d", res.CgroupVersion)
		}
		if res.MemoryLimitBytes != mem1GB {
			t.Errorf("expected MemoryLimitBytes %d, got %d", mem1GB, res.MemoryLimitBytes)
		}
		if res.CPUQuota != quota4CPUs {
			t.Errorf("expected CPUQuota %f, got %f", quota4CPUs, res.CPUQuota)
		}
	})
}

func TestAutoTune_CgroupV1_Success(t *testing.T) {
	const mem512MB int64 = 512 * 1024 * 1024
	const quota2CPUs float64 = 2.0
	const expectedCPUs int = 2

	mockLimits := cgroup.Limits{
		CgroupVersion:    cgroup.VersionV1,
		MemoryLimitBytes: mem512MB,
		CPUQuota:         quota2CPUs,
		CPUs:             expectedCPUs,
	}

	withIsolatedEnv(t, nil, &mockLimits, nil, func(memLimit *int64, maxProcs *int, _ *int, _ *bytes.Buffer) {
		res := AutoTune()

		expectedMemLimit, ok := cgroup.CalculateGOMEMLIMIT(mem512MB, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes)
		if !ok {
			t.Fatalf("CalculateGOMEMLIMIT failed unexpectedly")
		}

		if !res.MemLimitApplied || !res.MaxProcsApplied {
			t.Errorf("expected limits to be applied")
		}
		if res.GOMEMLIMIT != expectedMemLimit {
			t.Errorf("expected GOMEMLIMIT %d, got %d", expectedMemLimit, res.GOMEMLIMIT)
		}
		if res.GOMAXPROCS != expectedCPUs {
			t.Errorf("expected GOMAXPROCS %d, got %d", expectedCPUs, res.GOMAXPROCS)
		}
		if res.CgroupVersion != cgroup.VersionV1 {
			t.Errorf("expected CgroupVersion 1, got %d", res.CgroupVersion)
		}
	})
}

func TestAutoTune_ExplicitEnvOverrides(t *testing.T) {
	const mem1GB int64 = 1024 * 1024 * 1024
	const quota4CPUs float64 = 4.0
	const expectedCPUs int = 4

	mockLimits := cgroup.Limits{
		CgroupVersion:    cgroup.VersionV2,
		MemoryLimitBytes: mem1GB,
		CPUQuota:         quota4CPUs,
		CPUs:             expectedCPUs,
	}

	t.Run("ExplicitGOMEMLIMIT", func(t *testing.T) {
		mockEnv := map[string]string{
			"GOMEMLIMIT": "500MiB",
		}
		withIsolatedEnv(t, mockEnv, &mockLimits, nil, func(memLimit *int64, maxProcs *int, _ *int, _ *bytes.Buffer) {
			res := AutoTune()
			if res.MemLimitApplied {
				t.Errorf("expected MemLimitApplied to be false")
			}
			if *memLimit != -1 {
				t.Errorf("expected debug.SetMemoryLimit not to be called, got %d", *memLimit)
			}
			if !res.MaxProcsApplied {
				t.Errorf("expected MaxProcsApplied to be true")
			}
			if *maxProcs != expectedCPUs {
				t.Errorf("expected GOMAXPROCS %d, got %d", expectedCPUs, *maxProcs)
			}
		})
	})

	t.Run("ExplicitGOMAXPROCS", func(t *testing.T) {
		mockEnv := map[string]string{
			"GOMAXPROCS": "8",
		}
		withIsolatedEnv(t, mockEnv, &mockLimits, nil, func(memLimit *int64, maxProcs *int, _ *int, _ *bytes.Buffer) {
			res := AutoTune()
			if !res.MemLimitApplied {
				t.Errorf("expected MemLimitApplied to be true")
			}
			if res.MaxProcsApplied {
				t.Errorf("expected MaxProcsApplied to be false")
			}
			if *maxProcs != -1 {
				t.Errorf("expected runtime.GOMAXPROCS not to be called, got %d", *maxProcs)
			}
		})
	})

	t.Run("ExplicitGOGC", func(t *testing.T) {
		mockEnv := map[string]string{
			"GOGC":              "80",
			format.EnvGCProfile: testProfileLatencyCritical,
		}
		withIsolatedEnv(t, mockEnv, &mockLimits, nil, func(_ *int64, _ *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune()
			if res.GOGCApplied {
				t.Errorf("expected GOGCApplied to be false when explicit GOGC is set")
			}
			if *gogc != -999 {
				t.Errorf("expected debug.SetGCPercent not to be called, got %d", *gogc)
			}
		})
	})

	t.Run("AllExplicit", func(t *testing.T) {
		mockEnv := map[string]string{
			"GOMEMLIMIT": "500MiB",
			"GOMAXPROCS": "8",
			"GOGC":       "120",
		}
		withIsolatedEnv(t, mockEnv, &mockLimits, nil, func(memLimit *int64, maxProcs *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune()
			if res.MemLimitApplied || res.MaxProcsApplied || res.GOGCApplied {
				t.Errorf("expected all applied flags to be false")
			}
			if *memLimit != -1 || *maxProcs != -1 || *gogc != -999 {
				t.Errorf("expected neither limit to be set")
			}
		})
	})
}

func TestAutoTune_CustomMemoryRatio_Env(t *testing.T) {
	const mem1GB int64 = 1024 * 1024 * 1024
	mockLimits := cgroup.Limits{
		CgroupVersion:    cgroup.VersionV2,
		MemoryLimitBytes: mem1GB,
	}

	t.Run("ValidRatioEnv", func(t *testing.T) {
		mockEnv := map[string]string{
			format.EnvMemRatio: "0.80",
		}
		withIsolatedEnv(t, mockEnv, &mockLimits, nil, func(memLimit *int64, _ *int, _ *int, _ *bytes.Buffer) {
			res := AutoTune()
			expectedMemLimit, ok := cgroup.CalculateGOMEMLIMIT(mem1GB, 0.80, cgroup.DefaultMinHeadroomBytes)
			if !ok {
				t.Fatalf("CalculateGOMEMLIMIT failed")
			}
			if res.GOMEMLIMIT != expectedMemLimit || *memLimit != expectedMemLimit {
				t.Errorf("expected %d, got res=%d set=%d", expectedMemLimit, res.GOMEMLIMIT, *memLimit)
			}
		})
	})

	t.Run("InvalidRatioEnvFallsBack", func(t *testing.T) {
		mockEnv := map[string]string{
			format.EnvMemRatio: "invalid_number",
		}
		withIsolatedEnv(t, mockEnv, &mockLimits, nil, func(memLimit *int64, _ *int, _ *int, _ *bytes.Buffer) {
			res := AutoTune()
			expectedMemLimit, ok := cgroup.CalculateGOMEMLIMIT(mem1GB, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes)
			if !ok {
				t.Fatalf("CalculateGOMEMLIMIT failed")
			}
			if res.GOMEMLIMIT != expectedMemLimit {
				t.Errorf("expected fallback to %d, got %d", expectedMemLimit, res.GOMEMLIMIT)
			}
		})
	})
}

func TestAutoTune_WorkloadProfiles_Env(t *testing.T) {
	const mem1GB int64 = 1024 * 1024 * 1024
	mockLimits := cgroup.Limits{
		CgroupVersion:    cgroup.VersionV2,
		MemoryLimitBytes: mem1GB,
		CPUQuota:         4.0,
		CPUs:             4,
	}

	t.Run("LatencyCriticalEnv", func(t *testing.T) {
		mockEnv := map[string]string{
			format.EnvGCProfile: testProfileLatencyCritical,
		}
		withIsolatedEnv(t, mockEnv, &mockLimits, nil, func(_ *int64, _ *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune()
			if !res.GOGCApplied || *gogc != cgroup.DefaultLatencyCriticalGOGC {
				t.Errorf("expected GOGC=75, got res=%d set=%d applied=%t", res.GOGC, *gogc, res.GOGCApplied)
			}
			if res.ProfileApplied != string(ProfileLatencyCritical) {
				t.Errorf("expected profile %q, got %q", ProfileLatencyCritical, res.ProfileApplied)
			}
		})
	})

	t.Run("MemoryConstrainedEnv", func(t *testing.T) {
		mockEnv := map[string]string{
			format.EnvGCProfile: "memory_constrained",
		}
		withIsolatedEnv(t, mockEnv, &mockLimits, nil, func(memLimit *int64, _ *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune()
			if !res.GOGCApplied || *gogc != cgroup.DefaultMemoryConstrainedGOGC {
				t.Errorf("expected GOGC=40, got res=%d set=%d", res.GOGC, *gogc)
			}
			expectedMem, _ := cgroup.CalculateGOMEMLIMIT(mem1GB, cgroup.DefaultMemoryConstrainedRatio, cgroup.DefaultMinHeadroomBytes)
			if res.GOMEMLIMIT != expectedMem || *memLimit != expectedMem {
				t.Errorf("expected 80%% memory ratio limit %d, got %d", expectedMem, res.GOMEMLIMIT)
			}
		})
	})

	t.Run("BatchETLEnv", func(t *testing.T) {
		mockEnv := map[string]string{
			format.EnvGCProfile: "batch_etl",
		}
		withIsolatedEnv(t, mockEnv, &mockLimits, nil, func(_ *int64, _ *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune()
			if !res.GOGCApplied || *gogc != cgroup.DefaultBatchETLGOGC {
				t.Errorf("expected GOGC=-1, got res=%d set=%d", res.GOGC, *gogc)
			}
			if res.ProfileApplied != string(ProfileBatchETL) {
				t.Errorf("expected profile %q, got %q", ProfileBatchETL, res.ProfileApplied)
			}
		})
	})
}

func TestAutoTune_WorkloadProfiles_Options(t *testing.T) {
	const mem1GB int64 = 1024 * 1024 * 1024
	mockLimits := cgroup.Limits{
		CgroupVersion:    cgroup.VersionV2,
		MemoryLimitBytes: mem1GB,
		CPUQuota:         4.0,
		CPUs:             4,
	}

	t.Run("OptionLatencyCritical", func(t *testing.T) {
		withIsolatedEnv(t, nil, &mockLimits, nil, func(_ *int64, _ *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune(WithProfile(ProfileLatencyCritical))
			if !res.GOGCApplied || *gogc != cgroup.DefaultLatencyCriticalGOGC {
				t.Errorf("expected GOGC=75, got res=%d set=%d", res.GOGC, *gogc)
			}
		})
	})

	t.Run("OptionMemoryConstrained", func(t *testing.T) {
		withIsolatedEnv(t, nil, &mockLimits, nil, func(memLimit *int64, _ *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune(WithProfile(ProfileMemoryConstrained))
			if !res.GOGCApplied || *gogc != cgroup.DefaultMemoryConstrainedGOGC {
				t.Errorf("expected GOGC=40, got res=%d set=%d", res.GOGC, *gogc)
			}
		})
	})

	t.Run("OptionBatchETL", func(t *testing.T) {
		withIsolatedEnv(t, nil, &mockLimits, nil, func(_ *int64, _ *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune(WithProfile(ProfileBatchETL))
			if !res.GOGCApplied || *gogc != cgroup.DefaultBatchETLGOGC {
				t.Errorf("expected GOGC=-1, got res=%d set=%d", res.GOGC, *gogc)
			}
		})
	})

	t.Run("OptionExplicitGOGC", func(t *testing.T) {
		withIsolatedEnv(t, nil, &mockLimits, nil, func(_ *int64, _ *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune(WithGOGC(65))
			if !res.GOGCApplied || *gogc != 65 || res.GOGC != 65 {
				t.Errorf("expected explicit GOGC=65, got res=%d set=%d", res.GOGC, *gogc)
			}
		})
	})
}

func TestAutoTune_AdaptiveProfile(t *testing.T) {
	const (
		mem1GB       = int64(1024 * 1024 * 1024)
		liveHeap500M = int64(500 * 1024 * 1024)
	)
	mockLimits := cgroup.Limits{
		CgroupVersion:    cgroup.VersionV2,
		MemoryLimitBytes: mem1GB,
		CPUQuota:         4.0,
		CPUs:             4,
	}

	t.Run("AdaptiveWithOptionBytes", func(t *testing.T) {
		withIsolatedEnv(t, nil, &mockLimits, nil, func(_ *int64, _ *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune(
				WithProfile(ProfileAdaptive),
				WithLiveHeapEstimate(liveHeap500M),
			)
			if !res.GOGCApplied || *gogc <= 0 {
				t.Errorf("expected calculated GOGC applied, got %d (%d)", res.GOGC, *gogc)
			}
			if res.ProfileApplied != string(ProfileAdaptive) {
				t.Errorf("expected ProfileAdaptive, got %q", res.ProfileApplied)
			}
		})
	})

	t.Run("AdaptiveWithOptionString", func(t *testing.T) {
		withIsolatedEnv(t, nil, &mockLimits, nil, func(_ *int64, _ *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune(
				WithProfile(ProfileAdaptive),
				WithLiveHeapEstimateString("450MB"),
			)
			if !res.GOGCApplied || *gogc <= 0 {
				t.Errorf("expected calculated GOGC applied, got %d (%d)", res.GOGC, *gogc)
			}
		})
	})

	t.Run("AdaptiveWithEnvVars", func(t *testing.T) {
		mockEnv := map[string]string{
			format.EnvGCProfile:         "adaptive",
			format.EnvLiveHeapEstimate:  "350MiB",
		}
		withIsolatedEnv(t, mockEnv, &mockLimits, nil, func(_ *int64, _ *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune()
			if !res.GOGCApplied || *gogc <= 0 {
				t.Errorf("expected calculated GOGC applied, got %d (%d)", res.GOGC, *gogc)
			}
		})
	})

	t.Run("AdaptiveMissingLiveHeapSkipsGOGC", func(t *testing.T) {
		withIsolatedEnv(t, nil, &mockLimits, nil, func(_ *int64, _ *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune(WithProfile(ProfileAdaptive))
			if res.GOGCApplied || *gogc != -999 {
				t.Errorf("expected GOGC not to be applied when live heap is missing, got %d", *gogc)
			}
			if !strings.Contains(res.SkippedReason, "missing live heap estimate") {
				t.Errorf("expected missing live heap skipped reason, got %q", res.SkippedReason)
			}
		})
	})
}

func TestAutoTune_FunctionalOptions(t *testing.T) {
	const mem2GB int64 = 2 * 1024 * 1024 * 1024
	mockLimits := cgroup.Limits{
		CgroupVersion:    cgroup.VersionV2,
		MemoryLimitBytes: mem2GB,
		CPUQuota:         2.0,
		CPUs:             2,
	}

	t.Run("OptionsOverride", func(t *testing.T) {
		const customRatio = 0.85
		const customHeadroom int64 = 128 * 1024 * 1024
		var loggerOutput string
		var mu sync.Mutex

		customLogger := func(formatStr string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			loggerOutput = "logged: " + fmt.Sprintf(formatStr, args...)
		}

		withIsolatedEnv(t, nil, &mockLimits, nil, func(memLimit *int64, maxProcs *int, _ *int, _ *bytes.Buffer) {
			res := AutoTune(
				WithMemoryRatio(customRatio),
				WithMinHeadroom(customHeadroom),
				WithLogger(customLogger),
				nil, // test nil option safety
			)

			expectedMemLimit, ok := cgroup.CalculateGOMEMLIMIT(mem2GB, customRatio, customHeadroom)
			if !ok {
				t.Fatalf("CalculateGOMEMLIMIT failed")
			}

			if !res.MemLimitApplied {
				t.Errorf("expected MemLimitApplied to be true")
			}
			if res.GOMEMLIMIT != expectedMemLimit || *memLimit != expectedMemLimit {
				t.Errorf("expected GOMEMLIMIT %d, got res=%d set=%d", expectedMemLimit, res.GOMEMLIMIT, *memLimit)
			}
			if *maxProcs != 2 {
				t.Errorf("expected maxProcs 2, got %d", *maxProcs)
			}

			mu.Lock()
			if !strings.Contains(loggerOutput, "cgroup_version=2") {
				t.Errorf("expected custom logger to be called, got: %s", loggerOutput)
			}
			mu.Unlock()
		})
	})

	t.Run("InvalidOptionsPreserveDefaults", func(t *testing.T) {
		cfg := defaultConfig()
		WithMemoryRatio(-1.0)(cfg)
		if cfg.memoryRatio != cgroup.DefaultMemoryRatio {
			t.Errorf("expected default memory ratio, got %f", cfg.memoryRatio)
		}

		WithMemoryRatio(1.5)(cfg)
		if cfg.memoryRatio != cgroup.DefaultMemoryRatio {
			t.Errorf("expected default memory ratio, got %f", cfg.memoryRatio)
		}

		WithMinHeadroom(0)(cfg)
		if cfg.minHeadroomBytes != cgroup.DefaultMinHeadroomBytes {
			t.Errorf("expected default headroom, got %d", cfg.minHeadroomBytes)
		}

		WithMinHeadroom(-100)(cfg)
		if cfg.minHeadroomBytes != cgroup.DefaultMinHeadroomBytes {
			t.Errorf("expected default headroom, got %d", cfg.minHeadroomBytes)
		}
	})
}

func TestAutoTune_WithCgroupRoot_LiveFilesystem(t *testing.T) {
	tmpDir := t.TempDir()
	v2MemMax := filepath.Join(tmpDir, "memory.max")
	v2CPUMax := filepath.Join(tmpDir, "cpu.max")

	const testMemLimit = "1073741824"   // 1 GB
	const testCPUQuota = "200000 100000" // 2 CPUs

	if err := os.WriteFile(v2MemMax, []byte(testMemLimit+"\n"), testFilePerm); err != nil {
		t.Fatalf("writing memory.max: %v", err)
	}
	if err := os.WriteFile(v2CPUMax, []byte(testCPUQuota+"\n"), testFilePerm); err != nil {
		t.Fatalf("writing cpu.max: %v", err)
	}

	withIsolatedEnv(t, nil, nil, nil, func(memLimit *int64, maxProcs *int, _ *int, _ *bytes.Buffer) {
		res := AutoTune(WithCgroupRoot(tmpDir))

		if res.CgroupVersion != cgroup.VersionV2 {
			t.Errorf("expected cgroup v2, got %d", res.CgroupVersion)
		}
		if res.MemoryLimitBytes != int64(1073741824) {
			t.Errorf("expected memory limit 1073741824, got %d", res.MemoryLimitBytes)
		}
		if res.CPUQuota != 2.0 {
			t.Errorf("expected CPU quota 2.0, got %f", res.CPUQuota)
		}
		if !res.MemLimitApplied || !res.MaxProcsApplied {
			t.Errorf("expected limits applied")
		}
		if res.GOMAXPROCS != 2 || *maxProcs != 2 {
			t.Errorf("expected GOMAXPROCS 2, got res=%d set=%d", res.GOMAXPROCS, *maxProcs)
		}
	})
}

func TestAutoTune_DiagnosticsLogging(t *testing.T) {
	const mem1GB int64 = 1024 * 1024 * 1024
	mockLimits := cgroup.Limits{
		CgroupVersion:    cgroup.VersionV2,
		MemoryLimitBytes: mem1GB,
		CPUQuota:         4.0,
		CPUs:             4,
	}

	t.Run("JSONLogging", func(t *testing.T) {
		mockEnv := map[string]string{
			format.EnvLog:       "json",
			format.EnvGCProfile: testProfileLatencyCritical,
		}
		withIsolatedEnv(t, mockEnv, &mockLimits, nil, func(_ *int64, _ *int, _ *int, stderrBuf *bytes.Buffer) {
			_ = AutoTune()
			output := stderrBuf.String()
			if !strings.Contains(output, "[microfat]") {
				t.Fatalf("expected JSON log prefix [microfat], got: %s", output)
			}
			cleanJSON := strings.TrimPrefix(strings.TrimSpace(output), "[microfat] ")

			var telem Telemetry
			if err := json.Unmarshal([]byte(cleanJSON), &telem); err != nil {
				t.Fatalf("unmarshaling json telemetry: %v", err)
			}
			if telem.Event != eventRuntimeInit {
				t.Errorf("expected event %s, got %s", eventRuntimeInit, telem.Event)
			}
			if telem.CgroupVersion != cgroup.VersionV2 {
				t.Errorf("expected cgroup v2, got %d", telem.CgroupVersion)
			}
			if telem.CgroupMemLimitBytes != mem1GB {
				t.Errorf("expected %d, got %d", mem1GB, telem.CgroupMemLimitBytes)
			}
			if telem.CgroupCPUQuota != 4.0 {
				t.Errorf("expected 4.0, got %f", telem.CgroupCPUQuota)
			}
			if !telem.MemLimitApplied || !telem.MaxProcsApplied || !telem.GOGCApplied {
				t.Errorf("expected limits and gogc applied")
			}
			if telem.GOMAXPROCS != "4" {
				t.Errorf("expected GOMAXPROCS '4', got %q", telem.GOMAXPROCS)
			}
			if telem.GOGC != "75" {
				t.Errorf("expected GOGC '75', got %q", telem.GOGC)
			}
			if telem.ProfileApplied != testProfileLatencyCritical {
				t.Errorf("expected ProfileApplied %q, got %q", testProfileLatencyCritical, telem.ProfileApplied)
			}
		})
	})

	t.Run("DebugLogging", func(t *testing.T) {
		mockEnv := map[string]string{
			format.EnvDebug:     "1",
			format.EnvGCProfile: "batch_etl",
		}
		withIsolatedEnv(t, mockEnv, &mockLimits, nil, func(_ *int64, _ *int, _ *int, stderrBuf *bytes.Buffer) {
			_ = AutoTune()
			output := stderrBuf.String()
			if !strings.Contains(output, "[microfat:runtimeinit]") {
				t.Errorf("expected debug prefix, got: %s", output)
			}
			if !strings.Contains(output, "cgroup_v=2") || !strings.Contains(output, "gomaxprocs=4") || !strings.Contains(output, "gogc=off") {
				t.Errorf("expected debug output fields, got: %s", output)
			}
		})
	})

	t.Run("SilentByDefault", func(t *testing.T) {
		withIsolatedEnv(t, nil, &mockLimits, nil, func(_ *int64, _ *int, _ *int, stderrBuf *bytes.Buffer) {
			_ = AutoTune()
			if stderrBuf.Len() > 0 {
				t.Errorf("expected silent stderr, got: %s", stderrBuf.String())
			}
		})
	})
}

func TestResult_JSONSerialization(t *testing.T) {
	res := Result{
		CgroupVersion:    cgroup.VersionV2,
		MemoryLimitBytes: 1024 * 1024 * 1024,
		CPUQuota:         4.0,
		GOMEMLIMIT:       966367641,
		GOMAXPROCS:       4,
		GOGC:             75,
		ProfileApplied:   testProfileLatencyCritical,
		MemLimitApplied:  true,
		MaxProcsApplied:  true,
		GOGCApplied:      true,
		SkippedReason:    "",
	}

	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if !reflect.DeepEqual(res, decoded) {
		t.Errorf("expected %+v, got %+v", res, decoded)
	}
}
