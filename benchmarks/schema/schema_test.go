package schema_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const (
	testCores       = 8
	testSockets     = 1
	testNUMANodes   = 1
	testMemTotal    = uint64(16 * 1024 * 1024 * 1024)
	testMemAvail    = uint64(12 * 1024 * 1024 * 1024)
	testLimitBytes  = int64(4 * 1024 * 1024 * 1024)
	testCPUQuota    = int64(200000)
	testCPUPeriod   = int64(100000)
	testPID         = 12345
	testGOMAXPROCS  = 4
	testGOMEMLIMIT  = int64(2 * 1024 * 1024 * 1024)
	testDurationNs  = int64(1000000000)
	testOps         = int64(5000)
	testSample1     = int64(190000)
	testSample2     = int64(200000)
	testSample3     = int64(210000)
	testSampleCount = 3
	testMinNs       = int64(190000)
	testMaxNs       = int64(210000)
	testMeanNs      = 200000.0
	testStdDevNs    = 8164.9658
	testThroughput  = 5000.0
	testFreqKHz     = uint64(3600000)
)

func validMockExperiment() *schema.Experiment {
	cores := testCores
	sockets := testSockets
	numa := testNUMANodes
	memTotal := testMemTotal
	memAvail := testMemAvail
	cpuPeriod := testCPUPeriod
	gov := "performance"
	minFreq := testFreqKHz
	maxFreq := testFreqKHz
	configuredMem := "2GiB"
	effectiveMem := testGOMEMLIMIT
	configuredMaxProcs := testGOMAXPROCS
	envVal := "1"

	return &schema.Experiment{
		SchemaVersion: schema.SchemaVersion,
		ID:            "exp-baseline-20260907",
		Title:         "Authoritative Baseline Run",
		Description:   "Validates schema invariants and determinism",
		CreatedAt:     "2026-09-07T20:00:00.000000000Z",
		Environment: schema.EnvironmentSnapshot{
			Host: schema.HostInfo{
				OS:            "linux",
				Arch:          "amd64",
				KernelRelease: "6.8.0-generic",
				CPU: schema.CPUInfo{
					ModelName:       "AMD EPYC Processor",
					MicroarchLevel:  "v3",
					Cores:           &cores,
					Sockets:         &sockets,
					NUMANodes:       &numa,
					Flags:           []string{"avx2", "bmi2"},
					ScalingGovernor: &gov,
					MinFreqKHz:      &minFreq,
					MaxFreqKHz:      &maxFreq,
				},
				Memory: schema.MemoryInfo{
					TotalBytes:     &memTotal,
					AvailableBytes: &memAvail,
				},
				Cgroup: schema.CgroupInfo{
					Version:        "v2",
					MemoryMaxBytes: schema.NewFiniteLimit(testLimitBytes),
					CPUQuotaUs:     schema.NewFiniteLimit(testCPUQuota),
					CPUPeriodUs:    &cpuPeriod,
				},
			},
			Process: schema.ProcessContext{
				PID:                  testPID,
				ExecutablePath:       "/bin/microfat",
				Args:                 []string{"microfat", "benchmark"},
				GoVersion:            "go1.27.1",
				GOMAXPROCSConfigured: &configuredMaxProcs,
				GOMAXPROCSEffective:  testGOMAXPROCS,
				GOMEMLIMITConfigured: &configuredMem,
				GOMEMLIMITEffective:  &effectiveMem,
				EnvironmentVariables: []schema.EnvVar{
					{Name: "GODEBUG", Value: &envVal, IsSet: true},
					{Name: "MICROFAT_VERBOSE", Value: nil, IsSet: false},
				},
				InContainer:       false,
				RequestedAffinity: []int{0, 1, 2, 3},
				EffectiveAffinity: []int{0, 1, 2, 3},
			},
			Warnings: []string{},
		},
		Scenarios: []schema.ScenarioResult{
			{
				Name:     "baseline_arithmetic",
				Workload: "baseline",
				Observations: []schema.TrialObservations{
					{
						TrialIndex:   1,
						StartTime:    "2026-09-07T20:00:01.000000000Z",
						EndTime:      "2026-09-07T20:00:02.000000000Z",
						DurationNs:   testDurationNs,
						Operations:   testOps,
						RawSamplesNs: []int64{testSample1, testSample2, testSample3},
						Metrics: map[string]int64{
							"checksum": 42,
						},
					},
				},
				Analysis: schema.ScenarioAnalysis{
					AlgorithmVersion:    schema.AlgorithmVersionV1,
					PercentileMethod:    schema.PercentileMethodLinearR7,
					SampleCount:         testSampleCount,
					MinNs:               testMinNs,
					MaxNs:               testMaxNs,
					MeanNs:              testMeanNs,
					StdDevNs:            testStdDevNs,
					PercentilesNs:       map[string]float64{"p50": 200000.0, "p99": 210000.0},
					ThroughputOpsPerSec: testThroughput,
				},
			},
		},
	}
}

func TestResourceLimitValidation(t *testing.T) {
	t.Parallel()

	t.Run("finite with valid value", func(t *testing.T) {
		t.Parallel()
		lim := schema.NewFiniteLimit(testLimitBytes)
		if err := schema.ValidateResourceLimit(lim); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lim.State != schema.LimitStateFinite {
			t.Errorf("expected state %s, got %s", schema.LimitStateFinite, lim.State)
		}
		if lim.Value == nil || *lim.Value != testLimitBytes {
			t.Errorf("expected value %d, got %v", testLimitBytes, lim.Value)
		}
	})

	t.Run("finite with nil value fails", func(t *testing.T) {
		t.Parallel()
		lim := schema.ResourceLimit[int64]{State: schema.LimitStateFinite, Value: nil}
		if err := schema.ValidateResourceLimit(lim); err == nil {
			t.Fatal("expected error for finite state with nil value")
		}
	})

	t.Run("unlimited with nil value", func(t *testing.T) {
		t.Parallel()
		lim := schema.NewUnlimitedLimit[int64]()
		if err := schema.ValidateResourceLimit(lim); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lim.State != schema.LimitStateUnlimited {
			t.Errorf("expected state %s, got %s", schema.LimitStateUnlimited, lim.State)
		}
		if lim.Value != nil {
			t.Errorf("expected nil value, got %v", lim.Value)
		}
	})

	t.Run("unlimited with non-nil value fails", func(t *testing.T) {
		t.Parallel()
		v := testLimitBytes
		lim := schema.ResourceLimit[int64]{State: schema.LimitStateUnlimited, Value: &v}
		if err := schema.ValidateResourceLimit(lim); err == nil {
			t.Fatal("expected error for unlimited state with non-nil value")
		}
	})

	t.Run("unavailable with nil value", func(t *testing.T) {
		t.Parallel()
		lim := schema.NewUnavailableLimit[int64]()
		if err := schema.ValidateResourceLimit(lim); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lim.State != schema.LimitStateUnavailable {
			t.Errorf("expected state %s, got %s", schema.LimitStateUnavailable, lim.State)
		}
		if lim.Value != nil {
			t.Errorf("expected nil value, got %v", lim.Value)
		}
	})

	t.Run("unavailable with non-nil value fails", func(t *testing.T) {
		t.Parallel()
		v := testLimitBytes
		lim := schema.ResourceLimit[int64]{State: schema.LimitStateUnavailable, Value: &v}
		if err := schema.ValidateResourceLimit(lim); err == nil {
			t.Fatal("expected error for unavailable state with non-nil value")
		}
	})

	t.Run("invalid state fails", func(t *testing.T) {
		t.Parallel()
		lim := schema.ResourceLimit[int64]{State: "bogus", Value: nil}
		if err := schema.ValidateResourceLimit(lim); err == nil {
			t.Fatal("expected error for unknown limit state")
		}
	})
}

func TestExperimentValidation(t *testing.T) {
	t.Parallel()

	t.Run("valid experiment passes", func(t *testing.T) {
		t.Parallel()
		exp := validMockExperiment()
		if err := schema.ValidateExperiment(exp); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("valid experiment with RFC3339 seconds timestamp", func(t *testing.T) {
		t.Parallel()
		exp := validMockExperiment()
		exp.CreatedAt = "2026-09-07T20:00:00Z"
		exp.Scenarios[0].Observations[0].StartTime = "2026-09-07T20:00:01Z"
		exp.Scenarios[0].Observations[0].EndTime = "2026-09-07T20:00:02Z"
		if err := schema.ValidateExperiment(exp); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("nil experiment fails", func(t *testing.T) {
		t.Parallel()
		if err := schema.ValidateExperiment(nil); !errors.Is(err, schema.ErrNilExperiment) {
			t.Fatalf("expected ErrNilExperiment, got %v", err)
		}
	})

	t.Run("invalid schema version fails", func(t *testing.T) {
		t.Parallel()
		exp := validMockExperiment()
		exp.SchemaVersion = "v2"
		if err := schema.ValidateExperiment(exp); !errors.Is(err, schema.ErrInvalidSchemaVersion) {
			t.Fatalf("expected ErrInvalidSchemaVersion, got %v", err)
		}
	})

	t.Run("empty id fails", func(t *testing.T) {
		t.Parallel()
		exp := validMockExperiment()
		exp.ID = "   "
		if err := schema.ValidateExperiment(exp); !errors.Is(err, schema.ErrEmptyExperimentID) {
			t.Fatalf("expected ErrEmptyExperimentID, got %v", err)
		}
	})

	t.Run("invalid timestamp fails", func(t *testing.T) {
		t.Parallel()
		exp := validMockExperiment()
		exp.CreatedAt = "not-a-timestamp"
		if err := schema.ValidateExperiment(exp); !errors.Is(err, schema.ErrInvalidTimestamp) {
			t.Fatalf("expected ErrInvalidTimestamp, got %v", err)
		}
	})

	t.Run("invalid cgroup limits fail", func(t *testing.T) {
		t.Parallel()
		exp := validMockExperiment()
		exp.Environment.Host.Cgroup.MemoryMaxBytes = schema.ResourceLimit[int64]{
			State: schema.LimitStateFinite,
			Value: nil,
		}
		if err := schema.ValidateExperiment(exp); err == nil {
			t.Fatal("expected error for invalid memory limit")
		}

		exp2 := validMockExperiment()
		exp2.Environment.Host.Cgroup.CPUQuotaUs = schema.ResourceLimit[int64]{
			State: schema.LimitStateUnlimited,
			Value: &exp2.Scenarios[0].Observations[0].DurationNs,
		}
		if err := schema.ValidateExperiment(exp2); err == nil {
			t.Fatal("expected error for invalid cpu quota limit")
		}
	})

	t.Run("empty scenarios fails", func(t *testing.T) {
		t.Parallel()
		exp := validMockExperiment()
		exp.Scenarios = nil
		if err := schema.ValidateExperiment(exp); !errors.Is(err, schema.ErrEmptyScenarios) {
			t.Fatalf("expected ErrEmptyScenarios, got %v", err)
		}
	})

	t.Run("empty scenario name fails", func(t *testing.T) {
		t.Parallel()
		exp := validMockExperiment()
		exp.Scenarios[0].Name = ""
		if err := schema.ValidateExperiment(exp); !errors.Is(err, schema.ErrInvalidScenarioName) {
			t.Fatalf("expected ErrInvalidScenarioName, got %v", err)
		}
	})

	t.Run("empty scenario workload fails", func(t *testing.T) {
		t.Parallel()
		exp := validMockExperiment()
		exp.Scenarios[0].Workload = ""
		if err := schema.ValidateExperiment(exp); !errors.Is(err, schema.ErrInvalidScenarioName) {
			t.Fatalf("expected ErrInvalidScenarioName, got %v", err)
		}
	})

	t.Run("empty observations fails", func(t *testing.T) {
		t.Parallel()
		exp := validMockExperiment()
		exp.Scenarios[0].Observations = nil
		if err := schema.ValidateExperiment(exp); !errors.Is(err, schema.ErrEmptyObservations) {
			t.Fatalf("expected ErrEmptyObservations, got %v", err)
		}
	})

	t.Run("invalid observation metrics fail", func(t *testing.T) {
		t.Parallel()
		exp := validMockExperiment()
		exp.Scenarios[0].Observations[0].DurationNs = 0
		if err := schema.ValidateExperiment(exp); !errors.Is(err, schema.ErrInvalidDuration) {
			t.Fatalf("expected ErrInvalidDuration, got %v", err)
		}

		exp2 := validMockExperiment()
		exp2.Scenarios[0].Observations[0].Operations = -1
		if err := schema.ValidateExperiment(exp2); !errors.Is(err, schema.ErrNegativeOperations) {
			t.Fatalf("expected ErrNegativeOperations, got %v", err)
		}

		exp3 := validMockExperiment()
		exp3.Scenarios[0].Observations[0].RawSamplesNs = []int64{100, -50}
		if err := schema.ValidateExperiment(exp3); !errors.Is(err, schema.ErrNegativeSample) {
			t.Fatalf("expected ErrNegativeSample, got %v", err)
		}

		exp4 := validMockExperiment()
		exp4.Scenarios[0].Observations[0].StartTime = "invalid"
		if err := schema.ValidateExperiment(exp4); !errors.Is(err, schema.ErrInvalidTimestamp) {
			t.Fatalf("expected ErrInvalidTimestamp, got %v", err)
		}

		exp5 := validMockExperiment()
		exp5.Scenarios[0].Observations[0].EndTime = "invalid"
		if err := schema.ValidateExperiment(exp5); !errors.Is(err, schema.ErrInvalidTimestamp) {
			t.Fatalf("expected ErrInvalidTimestamp, got %v", err)
		}

		exp6 := validMockExperiment()
		exp6.Scenarios[0].Observations[0].TrialIndex = -1
		if err := schema.ValidateExperiment(exp6); err == nil {
			t.Fatal("expected error on negative trial index")
		}

		exp7 := validMockExperiment()
		exp7.Scenarios[0].Observations = append(exp7.Scenarios[0].Observations, exp7.Scenarios[0].Observations[0])
		if err := schema.ValidateExperiment(exp7); err == nil {
			t.Fatal("expected error on duplicate trial index")
		}

		exp8 := validMockExperiment()
		exp8.Scenarios[0].Observations[0].StartTime = "2026-09-07T20:00:05Z"
		exp8.Scenarios[0].Observations[0].EndTime = "2026-09-07T20:00:01Z"
		if err := schema.ValidateExperiment(exp8); err == nil {
			t.Fatal("expected error when end_time is before start_time")
		}

		exp9 := validMockExperiment()
		nonPosLimit := int64(0)
		exp9.Environment.Host.Cgroup.MemoryMaxBytes = schema.NewFiniteLimit(nonPosLimit)
		if err := schema.ValidateExperiment(exp9); err == nil {
			t.Fatal("expected error for non-positive finite memory limit")
		}

		exp10 := validMockExperiment()
		exp10.Environment.Host.Cgroup.CPUQuotaUs = schema.NewFiniteLimit(nonPosLimit)
		if err := schema.ValidateExperiment(exp10); err == nil {
			t.Fatal("expected error for non-positive finite cpu quota")
		}

		exp11 := validMockExperiment()
		nonPosPeriod := int64(0)
		exp11.Environment.Host.Cgroup.CPUPeriodUs = &nonPosPeriod
		if err := schema.ValidateExperiment(exp11); err == nil {
			t.Fatal("expected error for non-positive cpu period")
		}
	})

	t.Run("invalid scenario analysis fails", func(t *testing.T) {
		t.Parallel()
		exp := validMockExperiment()
		exp.Scenarios[0].Analysis.AlgorithmVersion = "v2"
		if err := schema.ValidateExperiment(exp); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis, got %v", err)
		}

		exp2 := validMockExperiment()
		exp2.Scenarios[0].Analysis.PercentileMethod = "nearest_rank"
		if err := schema.ValidateExperiment(exp2); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis, got %v", err)
		}

		exp3 := validMockExperiment()
		exp3.Scenarios[0].Analysis.SampleCount = 0
		if err := schema.ValidateExperiment(exp3); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis, got %v", err)
		}

		exp4 := validMockExperiment()
		exp4.Scenarios[0].Analysis.SampleCount = 999
		if err := schema.ValidateExperiment(exp4); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis, got %v", err)
		}

		exp5 := validMockExperiment()
		exp5.Scenarios[0].Analysis.MinNs = -1
		if err := schema.ValidateExperiment(exp5); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis, got %v", err)
		}

		exp6 := validMockExperiment()
		exp6.Scenarios[0].Analysis.MaxNs = exp6.Scenarios[0].Analysis.MinNs - 1
		if err := schema.ValidateExperiment(exp6); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis, got %v", err)
		}

		exp7 := validMockExperiment()
		exp7.Scenarios[0].Analysis.MeanNs = -1.0
		if err := schema.ValidateExperiment(exp7); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis, got %v", err)
		}

		exp8 := validMockExperiment()
		exp8.Scenarios[0].Analysis.PercentilesNs = nil
		if err := schema.ValidateExperiment(exp8); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis for nil percentiles, got %v", err)
		}

		exp9 := validMockExperiment()
		exp9.Scenarios[0].Analysis.PercentilesNs = map[string]float64{
			"p50": float64(exp9.Scenarios[0].Analysis.MaxNs + 5000),
		}
		if err := schema.ValidateExperiment(exp9); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis for out of bounds percentile, got %v", err)
		}

		// NaN and Inf checks
		expNaNMean := validMockExperiment()
		expNaNMean.Scenarios[0].Analysis.MeanNs = math.NaN()
		if err := schema.ValidateExperiment(expNaNMean); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis for NaN mean, got %v", err)
		}

		expInfStdDev := validMockExperiment()
		expInfStdDev.Scenarios[0].Analysis.StdDevNs = math.Inf(1)
		if err := schema.ValidateExperiment(expInfStdDev); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis for Inf stddev, got %v", err)
		}

		expNaNTP := validMockExperiment()
		expNaNTP.Scenarios[0].Analysis.ThroughputOpsPerSec = math.NaN()
		if err := schema.ValidateExperiment(expNaNTP); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis for NaN throughput, got %v", err)
		}

		expNaNP := validMockExperiment()
		expNaNP.Scenarios[0].Analysis.PercentilesNs = map[string]float64{
			"p50": math.NaN(),
		}
		if err := schema.ValidateExperiment(expNaNP); !errors.Is(err, schema.ErrInvalidAnalysis) {
			t.Fatalf("expected ErrInvalidAnalysis for NaN percentile, got %v", err)
		}
	})
}

func TestCanonicalSerializationAndEvidence(t *testing.T) {
	t.Parallel()

	exp1 := validMockExperiment()
	exp2 := validMockExperiment()

	bytes1, err1 := schema.SerializeCanonical(exp1)
	if err1 != nil {
		t.Fatalf("unexpected error: %v", err1)
	}
	if len(bytes1) == 0 {
		t.Fatal("serialized bytes cannot be empty")
	}

	bytes2, err2 := schema.SerializeCanonical(exp2)
	if err2 != nil {
		t.Fatalf("unexpected error: %v", err2)
	}

	if !bytes.Equal(bytes1, bytes2) {
		t.Fatal("deterministic serialization must yield exact identical bytes")
	}

	h1 := sha256.Sum256(bytes1)
	h2 := sha256.Sum256(bytes2)
	if hex.EncodeToString(h1[:]) != hex.EncodeToString(h2[:]) {
		t.Fatal("SHA256 digests must match")
	}

	t.Run("nil experiment serialization fails", func(t *testing.T) {
		t.Parallel()
		if _, err := schema.SerializeCanonical(nil); !errors.Is(err, schema.ErrNilExperiment) {
			t.Fatalf("expected ErrNilExperiment, got %v", err)
		}
	})

	t.Run("build evidence success and verification", func(t *testing.T) {
		t.Parallel()
		ev, err := schema.BuildEvidence(exp1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ev == nil {
			t.Fatal("evidence must not be nil")
		}
		if ev.SchemaVersion != schema.SchemaVersion {
			t.Errorf("expected version %s, got %s", schema.SchemaVersion, ev.SchemaVersion)
		}
		if ev.DigestSHA256 != hex.EncodeToString(h1[:]) {
			t.Errorf("expected digest %s, got %s", hex.EncodeToString(h1[:]), ev.DigestSHA256)
		}
		if !bytes.Equal(bytes1, ev.PayloadBytes) {
			t.Error("payload bytes mismatch")
		}
		if _, parseErr := time.Parse(time.RFC3339Nano, ev.GeneratedAt); parseErr != nil {
			t.Fatalf("invalid generated_at timestamp: %v", parseErr)
		}

		if err := schema.VerifyEvidence(ev); err != nil {
			t.Fatalf("unexpected verification error: %v", err)
		}

		// Test Experiment() extraction when Payload is present
		gotExp, err := ev.Experiment()
		if err != nil {
			t.Fatalf("unexpected Experiment() error: %v", err)
		}
		if gotExp.ID != exp1.ID {
			t.Errorf("expected ID %s, got %s", exp1.ID, gotExp.ID)
		}

		// Test Experiment() extraction when Payload is nil but PayloadBytes is populated
		evBytesOnly := *ev
		evBytesOnly.Payload = nil
		gotExp2, err := evBytesOnly.Experiment()
		if err != nil {
			t.Fatalf("unexpected Experiment() from bytes error: %v", err)
		}
		if gotExp2.ID != exp1.ID {
			t.Errorf("expected ID %s, got %s", exp1.ID, gotExp2.ID)
		}

		// Verify evidence when PayloadBytes is empty but Payload is populated
		evPayloadOnly := *ev
		evPayloadOnly.PayloadBytes = nil
		if err := schema.VerifyEvidence(&evPayloadOnly); err != nil {
			t.Fatalf("unexpected verification error with payload only: %v", err)
		}
	})

	t.Run("experiment extraction edge cases", func(t *testing.T) {
		t.Parallel()
		var nilEv *schema.Evidence
		if _, err := nilEv.Experiment(); err == nil {
			t.Fatal("expected error on nil evidence Experiment()")
		}

		emptyEv := &schema.Evidence{}
		if _, err := emptyEv.Experiment(); err == nil {
			t.Fatal("expected error on empty evidence Experiment()")
		}

		badJSONEv := &schema.Evidence{PayloadBytes: []byte("not valid json")}
		if _, err := badJSONEv.Experiment(); err == nil {
			t.Fatal("expected error on bad json Experiment()")
		}
	})

	t.Run("build evidence with invalid experiment fails", func(t *testing.T) {
		t.Parallel()
		badExp := validMockExperiment()
		badExp.ID = ""
		if _, err := schema.BuildEvidence(badExp); err == nil {
			t.Fatal("expected error building evidence for invalid experiment")
		}
	})

	t.Run("verify evidence failures", func(t *testing.T) {
		t.Parallel()
		if err := schema.VerifyEvidence(nil); err == nil {
			t.Fatal("expected error verifying nil evidence")
		}

		ev, err := schema.BuildEvidence(exp1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		badVersion := *ev
		badVersion.SchemaVersion = "v99"
		if err := schema.VerifyEvidence(&badVersion); !errors.Is(err, schema.ErrInvalidSchemaVersion) {
			t.Fatalf("expected ErrInvalidSchemaVersion, got %v", err)
		}

		corruptedPayload := *ev
		tamperedBytes := append([]byte(nil), ev.PayloadBytes...)
		tamperedBytes[0] = ' '
		corruptedPayload.PayloadBytes = tamperedBytes
		corruptedPayload.Payload = nil
		if err := schema.VerifyEvidence(&corruptedPayload); err == nil {
			t.Fatal("expected error verifying corrupted payload")
		}

		tamperedDigest := *ev
		tamperedDigest.DigestSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		if err := schema.VerifyEvidence(&tamperedDigest); err == nil {
			t.Fatal("expected error verifying tampered digest")
		}

		tamperedStruct := *ev
		tamperedExp := *exp1
		tamperedExp.Title = "Tampered Title"
		tamperedStruct.Payload = &tamperedExp
		if err := schema.VerifyEvidence(&tamperedStruct); err == nil {
			t.Fatal("expected error verifying tampered payload struct against payload bytes")
		}

		emptyEvidence := &schema.Evidence{
			SchemaVersion: schema.SchemaVersion,
			DigestSHA256:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			PayloadBytes:  nil,
			Payload:       nil,
		}
		if err := schema.VerifyEvidence(emptyEvidence); err == nil {
			t.Fatal("expected error verifying empty evidence with no payload")
		}
	})

	t.Run("evidence serialization roundtrip", func(t *testing.T) {
		t.Parallel()
		ev, err := schema.BuildEvidence(exp1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		rawJSON, err := schema.SerializeEvidence(ev)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rawJSON) == 0 {
			t.Fatal("serialized evidence JSON cannot be empty")
		}

		deserialized, err := schema.DeserializeEvidence(rawJSON)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := schema.VerifyEvidence(deserialized); err != nil {
			t.Fatalf("deserialized evidence verification failed: %v", err)
		}
		if ev.DigestSHA256 != deserialized.DigestSHA256 {
			t.Errorf("digest mismatch: %s vs %s", ev.DigestSHA256, deserialized.DigestSHA256)
		}
		if ev.SchemaVersion != deserialized.SchemaVersion {
			t.Errorf("version mismatch: %s vs %s", ev.SchemaVersion, deserialized.SchemaVersion)
		}

		if _, errNil := schema.SerializeEvidence(nil); errNil == nil {
			t.Fatal("expected error serializing nil evidence")
		}

		if _, errBadJSON := schema.DeserializeEvidence([]byte("invalid json")); errBadJSON == nil {
			t.Fatal("expected error deserializing invalid JSON")
		}
	})
}
