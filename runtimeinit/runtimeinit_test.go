package runtimeinit

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	testReasonDryRun           = "dry-run mode"
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

func TestAutoTune_CgroupV2_MemoryHighApplication(t *testing.T) {
	const (
		mem1GB    = int64(1024 * 1024 * 1024)
		mem2GB    = int64(2 * 1024 * 1024 * 1024)
		testCPUs  = 2
		testQuota = 2.0
	)

	t.Run("HighStricterThanMax_AppliesLimitFromHigh", func(t *testing.T) {
		mockLimits := cgroup.Limits{
			CgroupVersion:             cgroup.VersionV2,
			MemoryLimitBytes:          mem2GB,
			MemoryHighBytes:           mem1GB,
			EffectiveMemoryLimitBytes: mem1GB,
			CPUQuota:                  testQuota,
			CPUs:                      testCPUs,
		}

		withIsolatedEnv(t, nil, &mockLimits, nil, func(memLimit *int64, _ *int, _ *int, _ *bytes.Buffer) {
			res := AutoTune()

			expectedMemLimit, ok := cgroup.CalculateGOMEMLIMIT(mem1GB, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes)
			if !ok {
				t.Fatalf("CalculateGOMEMLIMIT failed unexpectedly")
			}

			if !res.MemLimitApplied {
				t.Errorf("expected memory limit to be applied")
			}
			if res.GOMEMLIMIT != expectedMemLimit || *memLimit != expectedMemLimit {
				t.Errorf("expected GOMEMLIMIT derived from high (%d), got res=%d set=%d", expectedMemLimit, res.GOMEMLIMIT, *memLimit)
			}
			if res.EffectiveMemoryLimitBytes != mem1GB {
				t.Errorf("expected EffectiveMemoryLimitBytes %d, got %d", mem1GB, res.EffectiveMemoryLimitBytes)
			}
			if res.ConstrainingLimit != cgroup.LimitConstraintHigh {
				t.Errorf("expected ConstrainingLimit %q, got %q", cgroup.LimitConstraintHigh, res.ConstrainingLimit)
			}
		})
	})

	t.Run("MaxUnlimited_HighConfigured_AppliesLimitFromHigh", func(t *testing.T) {
		mockLimits := cgroup.Limits{
			CgroupVersion:             cgroup.VersionV2,
			MemoryLimitBytes:          0,
			MemoryHighBytes:           mem1GB,
			EffectiveMemoryLimitBytes: mem1GB,
			CPUQuota:                  testQuota,
			CPUs:                      testCPUs,
		}

		withIsolatedEnv(t, nil, &mockLimits, nil, func(memLimit *int64, _ *int, _ *int, _ *bytes.Buffer) {
			res := AutoTune()

			expectedMemLimit, ok := cgroup.CalculateGOMEMLIMIT(mem1GB, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes)
			if !ok {
				t.Fatalf("CalculateGOMEMLIMIT failed unexpectedly")
			}

			if !res.MemLimitApplied {
				t.Errorf("expected memory limit to be applied")
			}
			if res.GOMEMLIMIT != expectedMemLimit || *memLimit != expectedMemLimit {
				t.Errorf("expected GOMEMLIMIT derived from high (%d), got res=%d set=%d", expectedMemLimit, res.GOMEMLIMIT, *memLimit)
			}
			if res.EffectiveMemoryLimitBytes != mem1GB {
				t.Errorf("expected EffectiveMemoryLimitBytes %d, got %d", mem1GB, res.EffectiveMemoryLimitBytes)
			}
			if res.ConstrainingLimit != cgroup.LimitConstraintHigh {
				t.Errorf("expected ConstrainingLimit %q, got %q", cgroup.LimitConstraintHigh, res.ConstrainingLimit)
			}
		})
	})

	t.Run("MaxEqualsHigh_ResolvesTieToMax", func(t *testing.T) {
		mockLimits := cgroup.Limits{
			CgroupVersion:             cgroup.VersionV2,
			MemoryLimitBytes:          mem1GB,
			MemoryHighBytes:           mem1GB,
			EffectiveMemoryLimitBytes: mem1GB,
			CPUQuota:                  testQuota,
			CPUs:                      testCPUs,
		}

		withIsolatedEnv(t, nil, &mockLimits, nil, func(memLimit *int64, _ *int, _ *int, _ *bytes.Buffer) {
			res := AutoTune()

			expectedMemLimit, ok := cgroup.CalculateGOMEMLIMIT(mem1GB, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes)
			if !ok {
				t.Fatalf("CalculateGOMEMLIMIT failed unexpectedly")
			}

			if !res.MemLimitApplied {
				t.Errorf("expected memory limit to be applied")
			}
			if res.GOMEMLIMIT != expectedMemLimit || *memLimit != expectedMemLimit {
				t.Errorf("expected GOMEMLIMIT %d, got res=%d set=%d", expectedMemLimit, res.GOMEMLIMIT, *memLimit)
			}
			if res.ConstrainingLimit != cgroup.LimitConstraintMax {
				t.Errorf("expected ConstrainingLimit %q on equal limits, got %q", cgroup.LimitConstraintMax, res.ConstrainingLimit)
			}
		})
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
	procFile := filepath.Join(tmpDir, "proc_cgroup")

	const testMemLimit = "1073741824"   // 1 GB
	const testCPUQuota = "200000 100000" // 2 CPUs

	if err := os.WriteFile(v2MemMax, []byte(testMemLimit+"\n"), testFilePerm); err != nil {
		t.Fatalf("writing memory.max: %v", err)
	}
	if err := os.WriteFile(v2CPUMax, []byte(testCPUQuota+"\n"), testFilePerm); err != nil {
		t.Fatalf("writing cpu.max: %v", err)
	}
	if err := os.WriteFile(procFile, []byte("0::/\n"), testFilePerm); err != nil {
		t.Fatalf("writing proc_cgroup: %v", err)
	}

	origReadFrom := readLimitsFromFunc
	readLimitsFromFunc = func(root string) (cgroup.Limits, error) {
		return cgroup.ReadLimitsCustom(root, procFile)
	}
	defer func() { readLimitsFromFunc = origReadFrom }()

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

func TestAutoTune_WithCgroupRoot_MemoryHighFilesystem(t *testing.T) {
	tmpDir := t.TempDir()
	v2MemMax := filepath.Join(tmpDir, "memory.max")
	v2MemHigh := filepath.Join(tmpDir, "memory.high")
	v2CPUMax := filepath.Join(tmpDir, "cpu.max")
	procFile := filepath.Join(tmpDir, "proc_cgroup")

	const (
		testMemHighBytes = int64(1073741824) // 1 GB
		testMemHighStr   = "1073741824"
		testCPUQuota     = "200000 100000" // 2 CPUs
		expectedCPUs     = 2
	)

	if err := os.WriteFile(v2MemMax, []byte("max\n"), testFilePerm); err != nil {
		t.Fatalf("writing memory.max: %v", err)
	}
	if err := os.WriteFile(v2MemHigh, []byte(testMemHighStr+"\n"), testFilePerm); err != nil {
		t.Fatalf("writing memory.high: %v", err)
	}
	if err := os.WriteFile(v2CPUMax, []byte(testCPUQuota+"\n"), testFilePerm); err != nil {
		t.Fatalf("writing cpu.max: %v", err)
	}
	if err := os.WriteFile(procFile, []byte("0::/\n"), testFilePerm); err != nil {
		t.Fatalf("writing proc_cgroup: %v", err)
	}

	origReadFrom := readLimitsFromFunc
	readLimitsFromFunc = func(root string) (cgroup.Limits, error) {
		return cgroup.ReadLimitsCustom(root, procFile)
	}
	defer func() { readLimitsFromFunc = origReadFrom }()

	withIsolatedEnv(t, nil, nil, nil, func(memLimit *int64, maxProcs *int, _ *int, _ *bytes.Buffer) {
		res := AutoTune(WithCgroupRoot(tmpDir))

		if res.CgroupVersion != cgroup.VersionV2 {
			t.Errorf("expected cgroup v2, got %d", res.CgroupVersion)
		}
		if res.MemoryLimitBytes != 0 {
			t.Errorf("expected memory limit 0 (unlimited), got %d", res.MemoryLimitBytes)
		}
		if res.MemoryHighBytes != testMemHighBytes {
			t.Errorf("expected memory high %d, got %d", testMemHighBytes, res.MemoryHighBytes)
		}
		if res.EffectiveMemoryLimitBytes != testMemHighBytes {
			t.Errorf("expected effective memory limit %d, got %d", testMemHighBytes, res.EffectiveMemoryLimitBytes)
		}
		if res.ConstrainingLimit != cgroup.LimitConstraintHigh {
			t.Errorf("expected ConstrainingLimit %q, got %q", cgroup.LimitConstraintHigh, res.ConstrainingLimit)
		}
		if !res.MemLimitApplied || !res.MaxProcsApplied {
			t.Errorf("expected limits applied")
		}
		if res.GOMAXPROCS != expectedCPUs || *maxProcs != expectedCPUs {
			t.Errorf("expected GOMAXPROCS %d, got res=%d set=%d", expectedCPUs, res.GOMAXPROCS, *maxProcs)
		}

		expectedMemLimit, ok := cgroup.CalculateGOMEMLIMIT(testMemHighBytes, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes)
		if !ok {
			t.Fatalf("CalculateGOMEMLIMIT failed unexpectedly")
		}
		if res.GOMEMLIMIT != expectedMemLimit || *memLimit != expectedMemLimit {
			t.Errorf("expected GOMEMLIMIT %d, got res=%d set=%d", expectedMemLimit, res.GOMEMLIMIT, *memLimit)
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
			format.EnvLog:       envValLogJSON,
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

	t.Run("JSONStructuredTelemetryLogging_MemoryHighConstraint", func(t *testing.T) {
		const (
			mem1GB = int64(1024 * 1024 * 1024)
			mem2GB = int64(2 * 1024 * 1024 * 1024)
		)
		mockHighLimits := cgroup.Limits{
			CgroupVersion:             cgroup.VersionV2,
			MemoryLimitBytes:          mem2GB,
			MemoryHighBytes:           mem1GB,
			EffectiveMemoryLimitBytes: mem1GB,
			CPUQuota:                  2.0,
			CPUs:                      2,
		}
		mockEnv := map[string]string{
			format.EnvLog: envValLogJSON,
		}
		withIsolatedEnv(t, mockEnv, &mockHighLimits, nil, func(_ *int64, _ *int, _ *int, stderrBuf *bytes.Buffer) {
			res := AutoTune()
			if res.ConstrainingLimit != cgroup.LimitConstraintHigh {
				t.Errorf("expected res.ConstrainingLimit 'high', got %q", res.ConstrainingLimit)
			}
			if res.MemoryHighBytes != mem1GB {
				t.Errorf("expected MemoryHighBytes %d, got %d", mem1GB, res.MemoryHighBytes)
			}
			if res.EffectiveMemoryLimitBytes != mem1GB {
				t.Errorf("expected EffectiveMemoryLimitBytes %d, got %d", mem1GB, res.EffectiveMemoryLimitBytes)
			}

			output := stderrBuf.String()
			cleanJSON := strings.TrimPrefix(strings.TrimSpace(output), "[microfat] ")
			var telem Telemetry
			if err := json.Unmarshal([]byte(cleanJSON), &telem); err != nil {
				t.Fatalf("unmarshaling json telemetry: %v", err)
			}
			if telem.ConstrainingLimit != cgroup.LimitConstraintHigh {
				t.Errorf("expected telem.ConstrainingLimit 'high', got %q", telem.ConstrainingLimit)
			}
			if telem.CgroupMemHighBytes != mem1GB {
				t.Errorf("expected CgroupMemHighBytes %d, got %d", mem1GB, telem.CgroupMemHighBytes)
			}
			if telem.EffectiveMemBytes != mem1GB {
				t.Errorf("expected EffectiveMemBytes %d, got %d", mem1GB, telem.EffectiveMemBytes)
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

func TestAutoTune_WithDryRun(t *testing.T) {
	const (
		mem1GB = 1024 * 1024 * 1024
		mem2GB = 2 * 1024 * 1024 * 1024
	)
	mockLimitsV2 := cgroup.Limits{
		CgroupVersion:    cgroup.VersionV2,
		MemoryLimitBytes: mem1GB,
		CPUQuota:         4.0,
		CPUs:             4,
	}

	t.Run("OptionWithDryRunTrue_CgroupV2", func(t *testing.T) {
		withIsolatedEnv(t, nil, &mockLimitsV2, nil, func(memLimit *int64, maxProcs *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune(WithDryRun(true))

			if !res.DryRun {
				t.Errorf("expected res.DryRun to be true")
			}
			if res.MemLimitApplied || res.MaxProcsApplied || res.GOGCApplied {
				t.Errorf("expected all applied flags to be false: mem=%t cpu=%t gogc=%t",
					res.MemLimitApplied, res.MaxProcsApplied, res.GOGCApplied)
			}
			if *memLimit != -1 {
				t.Errorf("expected setMemoryLimitFunc not called (-1), got %d", *memLimit)
			}
			if *maxProcs != -1 {
				t.Errorf("expected setMaxProcsFunc not called (-1), got %d", *maxProcs)
			}
			if *gogc != -999 {
				t.Errorf("expected setGCPercentFunc not called (-999), got %d", *gogc)
			}
			if res.GOMEMLIMIT <= 0 {
				t.Errorf("expected GOMEMLIMIT > 0, got %d", res.GOMEMLIMIT)
			}
			if res.GOMAXPROCS != 4 {
				t.Errorf("expected GOMAXPROCS 4, got %d", res.GOMAXPROCS)
			}
			if res.SkippedReason != testReasonDryRun {
				t.Errorf("expected SkippedReason %q, got %q", testReasonDryRun, res.SkippedReason)
			}
		})
	})

	t.Run("OptionWithDryRunTrue_CgroupV1", func(t *testing.T) {
		mockLimitsV1 := cgroup.Limits{
			CgroupVersion:    cgroup.VersionV1,
			MemoryLimitBytes: mem2GB,
			CPUQuota:         2.0,
			CPUs:             2,
		}
		withIsolatedEnv(t, nil, &mockLimitsV1, nil, func(memLimit *int64, maxProcs *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune(WithDryRun(true))

			if !res.DryRun {
				t.Errorf("expected res.DryRun to be true")
			}
			if res.MemLimitApplied || res.MaxProcsApplied || res.GOGCApplied {
				t.Errorf("expected all applied flags to be false: mem=%t cpu=%t gogc=%t",
					res.MemLimitApplied, res.MaxProcsApplied, res.GOGCApplied)
			}
			if *memLimit != -1 || *maxProcs != -1 || *gogc != -999 {
				t.Errorf("expected no runtime mutations: mem=%d maxProcs=%d gogc=%d", *memLimit, *maxProcs, *gogc)
			}
			if res.GOMEMLIMIT <= 0 || res.GOMAXPROCS != 2 {
				t.Errorf("expected planned values populated: mem=%d procs=%d", res.GOMEMLIMIT, res.GOMAXPROCS)
			}
			if res.SkippedReason != testReasonDryRun {
				t.Errorf("expected SkippedReason %q, got %q", testReasonDryRun, res.SkippedReason)
			}
		})
	})

	t.Run("WorkloadProfiles", func(t *testing.T) {
		const liveHeap = 150 * 1024 * 1024
		tests := []struct {
			name        string
			profile     Profile
			liveHeap    int64
			wantGOGC    int
			wantProfile string
			wantReason  string
		}{
			{
				name:        "LatencyCritical",
				profile:     ProfileLatencyCritical,
				wantGOGC:    75,
				wantProfile: string(ProfileLatencyCritical),
				wantReason:  testReasonDryRun,
			},
			{
				name:        "BatchETL",
				profile:     ProfileBatchETL,
				wantGOGC:    cgroup.DefaultBatchETLGOGC,
				wantProfile: string(ProfileBatchETL),
				wantReason:  testReasonDryRun,
			},
			{
				name:        "MemoryConstrained",
				profile:     ProfileMemoryConstrained,
				wantGOGC:    40,
				wantProfile: string(ProfileMemoryConstrained),
				wantReason:  testReasonDryRun,
			},
			{
				name:        "AdaptiveWithLiveHeap",
				profile:     ProfileAdaptive,
				liveHeap:    liveHeap,
				wantGOGC:    cgroup.DefaultLatencyCriticalGOGC,
				wantProfile: string(ProfileAdaptive),
				wantReason:  testReasonDryRun,
			},
			{
				name:        "AdaptiveMissingLiveHeap",
				profile:     ProfileAdaptive,
				liveHeap:    0,
				wantGOGC:    0,
				wantProfile: string(ProfileAdaptive),
				wantReason:  "adaptive GOGC tuning skipped (missing live heap estimate)",
			},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				opts := []Option{
					WithDryRun(true),
					WithProfile(tt.profile),
				}
				if tt.liveHeap > 0 {
					opts = append(opts, WithLiveHeapEstimate(tt.liveHeap))
				}

				withIsolatedEnv(t, nil, &mockLimitsV2, nil, func(_ *int64, _ *int, gogc *int, _ *bytes.Buffer) {
					res := AutoTune(opts...)

					if !res.DryRun {
						t.Errorf("expected res.DryRun to be true")
					}
					if res.MemLimitApplied || res.MaxProcsApplied || res.GOGCApplied {
						t.Errorf("expected all applied flags to be false")
					}
					if *gogc != -999 {
						t.Errorf("expected setGCPercentFunc not called, got %d", *gogc)
					}
					if tt.name == "AdaptiveWithLiveHeap" {
						if res.GOGC <= 0 {
							t.Errorf("expected adaptive GOGC > 0, got %d", res.GOGC)
						}
					} else if res.GOGC != tt.wantGOGC {
						t.Errorf("expected GOGC %d, got %d", tt.wantGOGC, res.GOGC)
					}
					if res.ProfileApplied != tt.wantProfile {
						t.Errorf("expected ProfileApplied %q, got %q", tt.wantProfile, res.ProfileApplied)
					}
					if res.SkippedReason != tt.wantReason {
						t.Errorf("expected SkippedReason %q, got %q", tt.wantReason, res.SkippedReason)
					}
				})
			})
		}
	})

	t.Run("ExplicitGOGCOption", func(t *testing.T) {
		t.Run("PositiveGOGC", func(t *testing.T) {
			withIsolatedEnv(t, nil, &mockLimitsV2, nil, func(_ *int64, _ *int, gogc *int, _ *bytes.Buffer) {
				const targetGOGC = 60
				res := AutoTune(WithDryRun(true), WithGOGC(targetGOGC))
				if !res.DryRun {
					t.Errorf("expected DryRun true")
				}
				if res.GOGC != targetGOGC {
					t.Errorf("expected res.GOGC %d, got %d", targetGOGC, res.GOGC)
				}
				if res.GOGCApplied {
					t.Errorf("expected GOGCApplied false")
				}
				if *gogc != -999 {
					t.Errorf("expected setGCPercentFunc not called, got %d", *gogc)
				}
			})
		})

		t.Run("ZeroGOGC", func(t *testing.T) {
			mockEnv := map[string]string{
				format.EnvLog: envValLogJSON,
			}
			withIsolatedEnv(t, mockEnv, &mockLimitsV2, nil, func(_ *int64, _ *int, gogc *int, stderrBuf *bytes.Buffer) {
				var customLogged string
				customLogger := func(formatStr string, args ...any) {
					customLogged = fmt.Sprintf(formatStr, args...)
				}

				res := AutoTune(WithDryRun(true), WithGOGC(0), WithLogger(customLogger))
				if !res.DryRun {
					t.Errorf("expected DryRun true")
				}
				if res.GOGC != 0 {
					t.Errorf("expected res.GOGC 0, got %d", res.GOGC)
				}
				if res.GOGCApplied {
					t.Errorf("expected GOGCApplied false")
				}
				if *gogc != -999 {
					t.Errorf("expected setGCPercentFunc not called, got %d", *gogc)
				}
				if !strings.Contains(customLogged, "gogc=0") {
					t.Errorf("expected custom logger to contain 'gogc=0', got %q", customLogged)
				}

				// Also verify JSON telemetry formatting for GOGC=0
				withIsolatedEnv(t, mockEnv, &mockLimitsV2, nil, func(_ *int64, _ *int, _ *int, stderrBufJSON *bytes.Buffer) {
					_ = AutoTune(WithDryRun(true), WithGOGC(0))
					cleanJSON := strings.TrimPrefix(strings.TrimSpace(stderrBufJSON.String()), "[microfat] ")
					var telem Telemetry
					if err := json.Unmarshal([]byte(cleanJSON), &telem); err != nil {
						t.Fatalf("unmarshaling json telemetry: %v", err)
					}
					if telem.GOGC != "0" {
						t.Errorf("expected telemetry GOGC '0', got %q", telem.GOGC)
					}
				})
			})
		})

		t.Run("NegativeOneGOGC_Off", func(t *testing.T) {
			mockEnv := map[string]string{
				format.EnvLog: envValLogJSON,
			}
			withIsolatedEnv(t, mockEnv, &mockLimitsV2, nil, func(_ *int64, _ *int, gogc *int, stderrBuf *bytes.Buffer) {
				res := AutoTune(WithDryRun(true), WithGOGC(-1))
				if res.GOGC != -1 {
					t.Errorf("expected res.GOGC -1, got %d", res.GOGC)
				}
				if *gogc != -999 {
					t.Errorf("expected setGCPercentFunc not called, got %d", *gogc)
				}
				cleanJSON := strings.TrimPrefix(strings.TrimSpace(stderrBuf.String()), "[microfat] ")
				var telem Telemetry
				if err := json.Unmarshal([]byte(cleanJSON), &telem); err != nil {
					t.Fatalf("unmarshaling json telemetry: %v", err)
				}
				if telem.GOGC != "off" {
					t.Errorf("expected telemetry GOGC 'off', got %q", telem.GOGC)
				}
			})
		})
	})

	t.Run("EnvVarActivation", func(t *testing.T) {
		testCases := []struct {
			name   string
			envVal string
		}{
			{name: "EnvOne", envVal: "1"},
			{name: "EnvTrueLower", envVal: "true"},
			{name: "EnvTrueUpper", envVal: "TRUE"},
			{name: "EnvTrueMixed", envVal: "True"},
			{name: "EnvWhitespaceOne", envVal: "  1  "},
			{name: "EnvWhitespaceTrue", envVal: "  true  "},
		}

		for _, tc := range testCases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				mockEnv := map[string]string{
					format.EnvDryRun: tc.envVal,
				}
				withIsolatedEnv(t, mockEnv, &mockLimitsV2, nil, func(memLimit *int64, maxProcs *int, gogc *int, _ *bytes.Buffer) {
					res := AutoTune()
					if !res.DryRun {
						t.Errorf("expected DryRun true for env %q", tc.envVal)
					}
					if *memLimit != -1 || *maxProcs != -1 || *gogc != -999 {
						t.Errorf("expected no runtime mutation under dry run env")
					}
					if res.MemLimitApplied || res.MaxProcsApplied || res.GOGCApplied {
						t.Errorf("expected applied flags to be false")
					}
					if res.SkippedReason != testReasonDryRun {
						t.Errorf("expected SkippedReason %q, got %q", testReasonDryRun, res.SkippedReason)
					}
				})
			})
		}
	})

	t.Run("Precedence_OptionOverridesEnv", func(t *testing.T) {
		t.Run("OptionFalseOverridesEnvTrue", func(t *testing.T) {
			mockEnv := map[string]string{
				format.EnvDryRun: "1",
			}
			withIsolatedEnv(t, mockEnv, &mockLimitsV2, nil, func(memLimit *int64, maxProcs *int, _ *int, _ *bytes.Buffer) {
				res := AutoTune(WithDryRun(false))
				if res.DryRun {
					t.Errorf("expected DryRun false when WithDryRun(false) is passed")
				}
				if !res.MemLimitApplied || !res.MaxProcsApplied {
					t.Errorf("expected limits to be applied when DryRun is overridden to false")
				}
				if *memLimit == -1 || *maxProcs == -1 {
					t.Errorf("expected runtime mutations when DryRun is false")
				}
			})
		})

		t.Run("OptionTrueOverridesEnvFalse", func(t *testing.T) {
			mockEnv := map[string]string{
				format.EnvDryRun: "0",
			}
			withIsolatedEnv(t, mockEnv, &mockLimitsV2, nil, func(memLimit *int64, maxProcs *int, _ *int, _ *bytes.Buffer) {
				res := AutoTune(WithDryRun(true))
				if !res.DryRun {
					t.Errorf("expected DryRun true when WithDryRun(true) is passed")
				}
				if res.MemLimitApplied || res.MaxProcsApplied {
					t.Errorf("expected applied flags false when WithDryRun(true) overrides env 0")
				}
				if *memLimit != -1 || *maxProcs != -1 {
					t.Errorf("expected no mutations")
				}
			})
		})
	})

	t.Run("DryRunWithCustomLogger", func(t *testing.T) {
		var loggedMsg string
		var mu sync.Mutex
		customLogger := func(formatStr string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			loggedMsg = fmt.Sprintf(formatStr, args...)
		}

		withIsolatedEnv(t, nil, &mockLimitsV2, nil, func(_ *int64, _ *int, _ *int, _ *bytes.Buffer) {
			_ = AutoTune(WithDryRun(true), WithLogger(customLogger))
			mu.Lock()
			defer mu.Unlock()
			if !strings.Contains(loggedMsg, "dry_run=true") {
				t.Errorf("expected logged message to contain 'dry_run=true', got %q", loggedMsg)
			}
			if !strings.Contains(loggedMsg, `skipped="`+testReasonDryRun+`"`) {
				t.Errorf("expected logged message to contain skipped dry-run mode, got %q", loggedMsg)
			}
		})
	})

	t.Run("DryRunWithJSONTelemetryLogging", func(t *testing.T) {
		mockEnv := map[string]string{
			format.EnvLog:       envValLogJSON,
			format.EnvGCProfile: testProfileLatencyCritical,
		}
		withIsolatedEnv(t, mockEnv, &mockLimitsV2, nil, func(_ *int64, _ *int, _ *int, stderrBuf *bytes.Buffer) {
			res := AutoTune(WithDryRun(true))
			if !res.DryRun {
				t.Fatalf("expected res.DryRun to be true")
			}

			output := stderrBuf.String()
			cleanJSON := strings.TrimPrefix(strings.TrimSpace(output), "[microfat] ")

			if !strings.Contains(cleanJSON, `"dry_run":true`) {
				t.Errorf("expected JSON to contain '\"dry_run\":true', got %s", cleanJSON)
			}

			var telem Telemetry
			if err := json.Unmarshal([]byte(cleanJSON), &telem); err != nil {
				t.Fatalf("unmarshaling json telemetry: %v", err)
			}
			if !telem.DryRun {
				t.Errorf("expected telem.DryRun to be true")
			}
			if telem.MemLimitApplied || telem.MaxProcsApplied || telem.GOGCApplied {
				t.Errorf("expected telemetry applied flags to be false")
			}
			if telem.GOGC != "75" {
				t.Errorf("expected telem.GOGC '75', got %q", telem.GOGC)
			}
			if telem.SkippedReason != testReasonDryRun {
				t.Errorf("expected telem.SkippedReason %q, got %q", testReasonDryRun, telem.SkippedReason)
			}
		})
	})

	t.Run("DryRunWithJSONTelemetryLogging_BatchETL", func(t *testing.T) {
		mockEnv := map[string]string{
			format.EnvLog:       envValLogJSON,
			format.EnvGCProfile: "batch_etl",
		}
		withIsolatedEnv(t, mockEnv, &mockLimitsV2, nil, func(_ *int64, _ *int, _ *int, stderrBuf *bytes.Buffer) {
			_ = AutoTune(WithDryRun(true))
			cleanJSON := strings.TrimPrefix(strings.TrimSpace(stderrBuf.String()), "[microfat] ")
			var telem Telemetry
			if err := json.Unmarshal([]byte(cleanJSON), &telem); err != nil {
				t.Fatalf("unmarshaling json telemetry: %v", err)
			}
			if telem.GOGC != "off" {
				t.Errorf("expected GOGC 'off' in telemetry for batch_etl, got %q", telem.GOGC)
			}
		})
	})

	t.Run("DryRunWithDebugLogging", func(t *testing.T) {
		mockEnv := map[string]string{
			format.EnvDebug: "1",
		}
		withIsolatedEnv(t, mockEnv, &mockLimitsV2, nil, func(_ *int64, _ *int, _ *int, stderrBuf *bytes.Buffer) {
			_ = AutoTune(WithDryRun(true))
			output := stderrBuf.String()
			if !strings.Contains(output, "dry_run=true") {
				t.Errorf("expected stderr to contain 'dry_run=true', got %s", output)
			}
			if !strings.Contains(output, `reason="`+testReasonDryRun+`"`) {
				t.Errorf("expected stderr to contain reason dry-run mode, got %s", output)
			}
		})
	})

	t.Run("AutoTuneDisabledUnderDryRun", func(t *testing.T) {
		mockEnv := map[string]string{
			format.EnvAutotune: "0",
			format.EnvDryRun:   "1",
		}
		withIsolatedEnv(t, mockEnv, &mockLimitsV2, nil, func(memLimit *int64, maxProcs *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune()
			if !res.DryRun {
				t.Errorf("expected DryRun true")
			}
			if !strings.Contains(res.SkippedReason, "auto-tuning disabled") {
				t.Errorf("expected SkippedReason to reflect disabled, got %q", res.SkippedReason)
			}
			if *memLimit != -1 || *maxProcs != -1 || *gogc != -999 {
				t.Errorf("expected no runtime calls")
			}
		})
	})

	t.Run("CgroupUnknownUnderDryRun", func(t *testing.T) {
		mockLimitsUnknown := cgroup.Limits{CgroupVersion: cgroup.VersionUnknown}
		withIsolatedEnv(t, nil, &mockLimitsUnknown, nil, func(memLimit *int64, maxProcs *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune(WithDryRun(true))
			if !res.DryRun {
				t.Errorf("expected DryRun true")
			}
			if !strings.Contains(res.SkippedReason, "cgroup resource limits not detected") {
				t.Errorf("expected SkippedReason 'cgroup resource limits not detected', got %q", res.SkippedReason)
			}
			if *memLimit != -1 || *maxProcs != -1 || *gogc != -999 {
				t.Errorf("expected no runtime calls")
			}
		})
	})

	t.Run("CgroupInspectionErrorUnderDryRun", func(t *testing.T) {
		inspectErr := errors.New("simulated error")
		withIsolatedEnv(t, nil, nil, inspectErr, func(memLimit *int64, maxProcs *int, gogc *int, _ *bytes.Buffer) {
			res := AutoTune(WithDryRun(true))
			if !res.DryRun {
				t.Errorf("expected DryRun true")
			}
			if !strings.Contains(res.SkippedReason, "cgroup inspection failed: simulated error") {
				t.Errorf("expected SkippedReason to contain simulated error, got %q", res.SkippedReason)
			}
			if *memLimit != -1 || *maxProcs != -1 || *gogc != -999 {
				t.Errorf("expected no runtime calls")
			}
		})
	})

	t.Run("ConcurrentDryRun", func(t *testing.T) {
		withIsolatedEnv(t, nil, &mockLimitsV2, nil, func(memLimit *int64, maxProcs *int, gogc *int, _ *bytes.Buffer) {
			const goroutines = 20
			var wg sync.WaitGroup
			wg.Add(goroutines)
			for i := 0; i < goroutines; i++ {
				go func() {
					defer wg.Done()
					res := AutoTune(WithDryRun(true), WithProfile(ProfileLatencyCritical))
					if !res.DryRun {
						t.Errorf("expected DryRun true")
					}
					if res.MemLimitApplied || res.MaxProcsApplied || res.GOGCApplied {
						t.Errorf("expected no applied flags in concurrent dry run")
					}
				}()
			}
			wg.Wait()
			if *memLimit != -1 || *maxProcs != -1 || *gogc != -999 {
				t.Errorf("expected no runtime mutations in concurrent dry run")
			}
		})
	})
}

func TestResult_JSONSerialization(t *testing.T) {
	const (
		mem1GB   = 1024 * 1024 * 1024
		mem512MB = 512 * 1024 * 1024
	)
	res := Result{
		CgroupVersion:             cgroup.VersionV2,
		MemoryLimitBytes:          mem1GB,
		MemoryHighBytes:           mem512MB,
		EffectiveMemoryLimitBytes: mem512MB,
		ConstrainingLimit:         cgroup.LimitConstraintHigh,
		CPUQuota:                  4.0,
		GOMEMLIMIT:                966367641,
		GOMAXPROCS:                4,
		GOGC:                      75,
		ProfileApplied:            testProfileLatencyCritical,
		MemLimitApplied:           true,
		MaxProcsApplied:           true,
		GOGCApplied:               true,
		SkippedReason:             "",
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

	t.Run("WithDryRunSerialization", func(t *testing.T) {
		dryRes := Result{
			CgroupVersion: cgroup.VersionV2,
			GOMEMLIMIT:    1024,
			DryRun:        true,
			SkippedReason: testReasonDryRun,
		}
		data, err := json.Marshal(dryRes)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		if !strings.Contains(string(data), `"dry_run":true`) {
			t.Errorf("expected '\"dry_run\":true' in JSON, got %s", string(data))
		}
		var dec Result
		if err := json.Unmarshal(data, &dec); err != nil {
			t.Fatalf("json.Unmarshal failed: %v", err)
		}
		if !dec.DryRun {
			t.Errorf("expected dec.DryRun to be true")
		}
	})

	t.Run("TelemetryJSONSerialization", func(t *testing.T) {
		telem := Telemetry{
			Event:         eventRuntimeInit,
			CgroupVersion: cgroup.VersionV2,
			DryRun:        true,
			GOMEMLIMIT:    "1024B",
			GOMAXPROCS:    "4",
			GOGC:          "75",
			SkippedReason: testReasonDryRun,
		}
		data, err := json.Marshal(telem)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		if !strings.Contains(string(data), `"dry_run":true`) {
			t.Errorf("expected '\"dry_run\":true' in Telemetry JSON, got %s", string(data))
		}
		var dec Telemetry
		if err := json.Unmarshal(data, &dec); err != nil {
			t.Fatalf("json.Unmarshal failed: %v", err)
		}
		if !dec.DryRun {
			t.Errorf("expected dec.DryRun to be true")
		}
	})
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	root := filepath.Clean(filepath.Join(wd, ".."))
	goModPath := filepath.Join(root, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		t.Fatalf("expected go.mod at %s", goModPath)
	}
	return root
}

func runFixture(t *testing.T, repoRoot string, fixtureRelPath string, extraEnv []string) (string, string, error) {
	t.Helper()

	targetPath := filepath.Join(repoRoot, fixtureRelPath)
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		t.Fatalf("expected fixture at %s", targetPath)
	}

	cmd := exec.Command("go", "run", targetPath)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	cmd.Env = append(cmd.Env, extraEnv...)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	return stdoutBuf.String(), stderrBuf.String(), err
}

func TestRuntimeInit_NoSideEffectsOnImport(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	stdout, stderr, err := runFixture(t, repoRoot, "runtimeinit/testdata/programmatic_app/main.go", []string{
		format.EnvDebug + "=1",
		format.EnvAutotune + "=1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v, stderr: %s", err, stderr)
	}

	if !strings.Contains(strings.TrimSpace(stdout), "programmatic ok") {
		t.Errorf("expected stdout 'programmatic ok', got: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("expected empty stderr on import of runtimeinit, got: %s", stderr)
	}
}

func TestRuntimeInit_ProgrammaticSubprocess(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	stdout, stderr, err := runFixture(t, repoRoot, "runtimeinit/testdata/custom_app/main.go", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v, stderr: %s", err, stderr)
	}

	if !strings.Contains(stdout, "profile=latency_critical gogc=75") {
		t.Errorf("expected 'profile=latency_critical gogc=75' in stdout, got: %s", stdout)
	}
}

func TestExecutable(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}

	tests := []struct {
		name        string
		envVal      string
		mockExec    func() (string, error)
		mockAbs     func(string) (string, error)
		wantPath    string
		wantErr     bool
		wantErrText string
	}{
		{
			name:     "OriginalExeAbsolutePath",
			envVal:   "/usr/local/bin/myapp",
			wantPath: "/usr/local/bin/myapp",
		},
		{
			name:     "OriginalExeRelativePath",
			envVal:   "bin/myapp",
			wantPath: filepath.Join(wd, "bin", "myapp"),
		},
		{
			name:     "OriginalExeDotSegments",
			envVal:   "/opt/app/../app/./bin/myapp",
			wantPath: "/opt/app/bin/myapp",
		},
		{
			name:     "OriginalExeAbsError_FallsBackToClean",
			envVal:   "foo/../bar",
			mockAbs:  func(string) (string, error) { return "", errors.New("simulated abs error") },
			wantPath: "bar",
		},
		{
			name:     "OriginalExeWhitespaceTrimmed",
			envVal:   "   /usr/local/bin/myapp   ",
			wantPath: "/usr/local/bin/myapp",
		},
		{
			name:     "OriginalExeWhitespaceOnly_FallsBackToOsExecutable",
			envVal:   "   \t\n ",
			mockExec: func() (string, error) {
				return "/fallback/binary", nil
			},
			wantPath: "/fallback/binary",
		},
		{
			name:   "FallbackToOsExecutableSuccess",
			envVal: "",
			mockExec: func() (string, error) {
				return "/fallback/binary", nil
			},
			wantPath: "/fallback/binary",
		},
		{
			name:   "FallbackToOsExecutableError",
			envVal: "",
			mockExec: func() (string, error) {
				return "", errors.New("cannot determine executable")
			},
			wantErr:     true,
			wantErrText: "cannot determine executable",
		},
		{
			name:   "FallbackToDefaultOsExecutable",
			envVal: "",
			mockExec: nil, // exercises default executableFunc (os.Executable)
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			origExec := executableFunc
			origAbs := absFunc
			defer func() {
				executableFunc = origExec
				absFunc = origAbs
			}()

			if tt.mockExec != nil {
				executableFunc = tt.mockExec
			}
			if tt.mockAbs != nil {
				absFunc = tt.mockAbs
			}

			mockEnv := make(map[string]string)
			if tt.envVal != "" {
				mockEnv[format.EnvOriginalExe] = tt.envVal
			}

			withIsolatedEnv(t, mockEnv, nil, nil, func(_ *int64, _ *int, _ *int, _ *bytes.Buffer) {
				got, err := Executable()
				if tt.wantErr {
					if err == nil {
						t.Fatalf("expected error containing %q, got nil", tt.wantErrText)
					}
					if !strings.Contains(err.Error(), tt.wantErrText) {
						t.Errorf("expected error %q, got %v", tt.wantErrText, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tt.mockExec == nil && tt.envVal == "" {
					expected, expErr := os.Executable()
					if expErr != nil {
						t.Fatalf("os.Executable failed: %v", expErr)
					}
					if got != expected {
						t.Errorf("got %q, want %q from os.Executable", got, expected)
					}
					return
				}
				if got != tt.wantPath {
					t.Errorf("got %q, want %q", got, tt.wantPath)
				}
			})
		})
	}
}

func TestRuntimeInit_ExecutableSubprocess(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	fixturePath := "runtimeinit/testdata/executable_app/main.go"

	tests := []struct {
		name       string
		env        []string
		wantPrefix string
		wantExact  string
	}{
		{
			name:      "ExplicitAbsolutePath",
			env:       []string{format.EnvOriginalExe + "=/opt/custom/bin/app"},
			wantExact: "/opt/custom/bin/app",
		},
		{
			name:      "RelativePathNormalized",
			env:       []string{format.EnvOriginalExe + "=bin/app"},
			wantExact: filepath.Join(repoRoot, "bin", "app"),
		},
		{
			name:       "FallbackWhenUnset",
			env:        nil,
			wantPrefix: "/",
		},
		{
			name:       "FallbackWhenWhitespaceOnly",
			env:        []string{format.EnvOriginalExe + "=   \t  "},
			wantPrefix: "/",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, err := runFixture(t, repoRoot, fixturePath, tt.env)
			if err != nil {
				t.Fatalf("runFixture failed: %v, stderr: %s", err, stderr)
			}
			trimmed := strings.TrimSpace(stdout)
			if tt.wantExact != "" && trimmed != tt.wantExact {
				t.Errorf("got stdout %q, want %q", trimmed, tt.wantExact)
			}
			if tt.wantPrefix != "" && !strings.HasPrefix(trimmed, tt.wantPrefix) {
				t.Errorf("got stdout %q, expected prefix %q", trimmed, tt.wantPrefix)
			}
		})
	}
}

