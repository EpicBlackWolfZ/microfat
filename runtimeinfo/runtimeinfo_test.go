package runtimeinfo

import (
	"math"
	"os/exec"
	"runtime/metrics"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testDigest       = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testGCPercent    = "75"
	testHostArchKey  = "MICROFAT_HOST_ARCH"
	testDigestKey    = "MICROFAT_SELECTED_SHA256"
	testGCProfileKey = "MICROFAT_CGROUP_GC_PROFILE"
)

func lookupMap(values map[string]string) lookupEnv {
	return func(key string) (string, bool) {
		value, present := values[key]
		return value, present
	}
}

func presentField[T any](value T, raw, source string) Field[T] {
	return Field[T]{Value: value, Raw: raw, Source: source, Status: StatusPresent}
}

func TestLauncherContract(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"MICROFAT_SELECTED_VARIANT":             "v3",
		testHostArchKey:                         "amd64",
		"MICROFAT_HOST_LEVEL":                   "v4",
		"MICROFAT_EXEC_MODE":                    "memfd",
		"MICROFAT_DISPATCH_MODE":                "cache",
		testDigestKey:                           strings.ToUpper(testDigest),
		"MICROFAT_SELECTED_SIZE":                "7389447",
		"MICROFAT_ORIGINAL_EXE":                 "/opt/my app/bin/app ",
		"MICROFAT_POLICY_APPLIED":               "safe_avx512",
		"MICROFAT_OVERRIDE_REASON":              "Intel Skylake-X downclock protection",
		"MICROFAT_CGROUP_VERSION":               "2",
		"MICROFAT_CGROUP_LIMIT_BYTES":           "1073741824",
		"MICROFAT_CGROUP_HIGH_BYTES":            "805306368",
		"MICROFAT_CGROUP_EFFECTIVE_LIMIT_BYTES": "805306368",
		"MICROFAT_CGROUP_CPUS":                  "2.50",
		"MICROFAT_CGROUP_GOMEMLIMIT":            "724775731B",
		"MICROFAT_CGROUP_GOMAXPROCS":            "2",
		"MICROFAT_CGROUP_GOGC":                  testGCPercent,
		testGCProfileKey:                        "latency_critical",
		"MICROFAT_CGROUP_GOGC_SKIPPED_REASON":   "retained informational reason",
		"MICROFAT_FUTURE_FIELD":                 "ignored",
	}
	want := LauncherHints{
		SelectedVariant:    presentField("v3", "v3", "MICROFAT_SELECTED_VARIANT"),
		HostArch:           presentField("amd64", "amd64", testHostArchKey),
		HostLevel:          presentField("v4", "v4", "MICROFAT_HOST_LEVEL"),
		ExecMode:           presentField("memfd", "memfd", "MICROFAT_EXEC_MODE"),
		DispatchMode:       presentField("cache", "cache", "MICROFAT_DISPATCH_MODE"),
		SelectedSHA256:     presentField(testDigest, strings.ToUpper(testDigest), testDigestKey),
		SelectedSizeBytes:  presentField(int64(7389447), "7389447", "MICROFAT_SELECTED_SIZE"),
		OriginalExecutable: presentField("/opt/my app/bin/app ", "/opt/my app/bin/app ", "MICROFAT_ORIGINAL_EXE"),
		PolicyApplied:      presentField("safe_avx512", "safe_avx512", "MICROFAT_POLICY_APPLIED"),
		OverrideReason: presentField("Intel Skylake-X downclock protection", "Intel Skylake-X downclock protection",
			"MICROFAT_OVERRIDE_REASON"),
		Cgroup: CgroupHints{
			Version:                   presentField(int64(2), "2", "MICROFAT_CGROUP_VERSION"),
			MemoryLimitBytes:          presentField(int64(1073741824), "1073741824", "MICROFAT_CGROUP_LIMIT_BYTES"),
			MemoryHighBytes:           presentField(int64(805306368), "805306368", "MICROFAT_CGROUP_HIGH_BYTES"),
			EffectiveMemoryLimitBytes: presentField(int64(805306368), "805306368", "MICROFAT_CGROUP_EFFECTIVE_LIMIT_BYTES"),
			CPUQuota:                  presentField(2.5, "2.50", "MICROFAT_CGROUP_CPUS"),
			RecommendedGOMEMLIMIT:     presentField("724775731B", "724775731B", "MICROFAT_CGROUP_GOMEMLIMIT"),
			RecommendedGOMAXPROCS:     presentField(int64(2), "2", "MICROFAT_CGROUP_GOMAXPROCS"),
			RecommendedGOGC:           presentField(int64(75), testGCPercent, "MICROFAT_CGROUP_GOGC"),
			GCProfile:                 presentField("latency_critical", "latency_critical", testGCProfileKey),
			GOGCSkippedReason: presentField("retained informational reason", "retained informational reason",
				"MICROFAT_CGROUP_GOGC_SKIPPED_REASON"),
		},
	}
	require.Equal(t, want, readLauncher(lookupMap(values)))
}

func TestConfiguredContract(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"GOMEMLIMIT": "512MiB", "GOMAXPROCS": "3", "GOGC": "off",
		"MICROFAT_CPU_POLICY": "native", "MICROFAT_GC_PROFILE": "batch_etl",
		"MICROFAT_AUTOTUNE": "false", "MICROFAT_DRY_RUN": "1",
		"MICROFAT_MEM_RATIO": "0.85", "MICROFAT_LIVE_HEAP_ESTIMATE": "150MiB",
	}
	want := ConfiguredEnvironment{
		GOMEMLIMIT:       presentField("512MiB", "512MiB", "GOMEMLIMIT"),
		GOMAXPROCS:       presentField("3", "3", "GOMAXPROCS"),
		GOGC:             presentField("off", "off", "GOGC"),
		CPUPolicy:        presentField("native", "native", "MICROFAT_CPU_POLICY"),
		GCProfile:        presentField("batch_etl", "batch_etl", "MICROFAT_GC_PROFILE"),
		Autotune:         presentField("false", "false", "MICROFAT_AUTOTUNE"),
		DryRun:           presentField("1", "1", "MICROFAT_DRY_RUN"),
		MemoryRatio:      presentField("0.85", "0.85", "MICROFAT_MEM_RATIO"),
		LiveHeapEstimate: presentField("150MiB", "150MiB", "MICROFAT_LIVE_HEAP_ESTIMATE"),
	}
	require.Equal(t, want, readConfigured(lookupMap(values)))
}

func TestMissingAndOpaqueFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		values     map[string]string
		wantStatus Status
		wantText   string
	}{
		{name: "absent", wantStatus: StatusMissing},
		{name: "explicit empty", values: map[string]string{"key": ""}, wantStatus: StatusPresent},
		{name: "whitespace preserved", values: map[string]string{"key": " \t "}, wantStatus: StatusPresent, wantText: " \t "},
		{name: "unsupported text preserved", values: map[string]string{"key": "future-setting"},
			wantStatus: StatusPresent, wantText: "future-setting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, Field[string]{Value: tc.wantText, Raw: tc.wantText, Status: tc.wantStatus, Source: "key"},
				textField(lookupMap(tc.values), "key"))
		})
	}
	configured := readConfigured(lookupMap(map[string]string{"GOMEMLIMIT": "", "MICROFAT_CPU_POLICY": "future-setting"}))
	assert.Equal(t, StatusPresent, configured.GOMEMLIMIT.Status)
	assert.Empty(t, configured.GOMEMLIMIT.Value)
	assert.Equal(t, "future-setting", configured.CPUPolicy.Value)
	assert.Equal(t, StatusMissing, configured.GOGC.Status)
	launcher := readLauncher(lookupMap(nil))
	assert.Equal(t, StatusMissing, launcher.SelectedVariant.Status)
	assert.Equal(t, StatusMissing, launcher.HostArch.Status)
	assert.Equal(t, StatusMissing, launcher.SelectedSHA256.Status)
	assert.Equal(t, StatusMissing, launcher.SelectedSizeBytes.Status)
	assert.Equal(t, StatusMissing, launcher.Cgroup.Version.Status)
	assert.Equal(t, StatusMissing, launcher.Cgroup.CPUQuota.Status)
}

func TestNumericParsing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw  string
		minimum    int64
		wantStatus Status
		want       int64
	}{
		{name: "zero bytes", raw: "0", wantStatus: StatusPresent},
		{name: "positive", raw: "4096", minimum: 1, wantStatus: StatusPresent, want: 4096},
		{name: "leading zeroes", raw: "001", minimum: 1, wantStatus: StatusPresent, want: 1},
		{name: "maximum", raw: "9223372036854775807", wantStatus: StatusPresent, want: math.MaxInt64},
		{name: "empty", raw: "", wantStatus: StatusInvalid},
		{name: "negative", raw: "-1", wantStatus: StatusInvalid},
		{name: "plus", raw: "+1", wantStatus: StatusInvalid},
		{name: "space", raw: " 1", wantStatus: StatusInvalid},
		{name: "fraction", raw: "1.5", wantStatus: StatusInvalid},
		{name: "non ascii digits", raw: "１２", wantStatus: StatusInvalid},
		{name: "exponent", raw: "1e3", wantStatus: StatusInvalid},
		{name: "zero below minimum", raw: "0", minimum: 1, wantStatus: StatusInvalid},
		{name: "overflow", raw: "9223372036854775808", wantStatus: StatusInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, Field[int64]{Value: tc.want, Raw: tc.raw, Status: tc.wantStatus, Source: "count"},
				integerField(lookupMap(map[string]string{"count": tc.raw}), "count", tc.minimum))
		})
	}
}

func TestQuotaParsing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw  string
		wantStatus Status
		want       float64
	}{
		{name: "zero ambiguous", raw: "0.00", wantStatus: StatusPresent},
		{name: "whole", raw: "4", wantStatus: StatusPresent, want: 4},
		{name: "fraction", raw: "0.25", wantStatus: StatusPresent, want: 0.25},
		{name: "empty", wantStatus: StatusInvalid},
		{name: "negative", raw: "-2.50", wantStatus: StatusInvalid},
		{name: "nan", raw: "NaN", wantStatus: StatusInvalid},
		{name: "infinity", raw: "Inf", wantStatus: StatusInvalid},
		{name: "plus infinity", raw: "+Inf", wantStatus: StatusInvalid},
		{name: "overflow", raw: strings.Repeat("9", 400), wantStatus: StatusInvalid},
		{name: "missing whole", raw: ".5", wantStatus: StatusInvalid},
		{name: "missing fraction", raw: "1.", wantStatus: StatusInvalid},
		{name: "multiple dots", raw: "1.2.3", wantStatus: StatusInvalid},
		{name: "space", raw: " 1.5", wantStatus: StatusInvalid},
		{name: "exponent", raw: "1e3", wantStatus: StatusInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, Field[float64]{Value: tc.want, Raw: tc.raw, Status: tc.wantStatus, Source: "quota"},
				quotaField(lookupMap(map[string]string{"quota": tc.raw}), "quota"))
		})
	}
}

func TestSupportedEnumsAndDigest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, key, raw string
		wantStatus     Status
		want           string
	}{
		{"arm host", testHostArchKey, "arm64", StatusPresent, "arm64"},
		{"future host", testHostArchKey, "riscv64", StatusUnsupported, ""},
		{"empty host", testHostArchKey, "", StatusUnsupported, ""},
		{"auto is request only", "MICROFAT_EXEC_MODE", "auto", StatusUnsupported, ""},
		{"future execution", "MICROFAT_EXEC_MODE", "future", StatusUnsupported, ""},
		{"uppercase mode", "MICROFAT_DISPATCH_MODE", "MEMFD", StatusUnsupported, ""},
		{"future profile", testGCProfileKey, "future", StatusUnsupported, ""},
		{"default profile", testGCProfileKey, "default", StatusPresent, "default"},
		{"memory profile", testGCProfileKey, "memory_constrained", StatusPresent, "memory_constrained"},
		{"batch profile", testGCProfileKey, "batch_etl", StatusPresent, "batch_etl"},
		{"adaptive profile", testGCProfileKey, "adaptive", StatusPresent, "adaptive"},
		{"uppercase digest", testDigestKey, strings.ToUpper(testDigest), StatusPresent, testDigest},
		{"short digest", testDigestKey, "abcd", StatusInvalid, ""},
		{"invalid hex", testDigestKey, strings.Repeat("z", 64), StatusInvalid, ""},
		{"odd digest length", testDigestKey, testDigest + "a", StatusInvalid, ""},
		{"empty digest", testDigestKey, "", StatusInvalid, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			launcher := readLauncher(lookupMap(map[string]string{tc.key: tc.raw}))
			fields := map[string]Field[string]{
				testHostArchKey:          launcher.HostArch,
				"MICROFAT_EXEC_MODE":     launcher.ExecMode,
				"MICROFAT_DISPATCH_MODE": launcher.DispatchMode,
				testGCProfileKey:         launcher.Cgroup.GCProfile,
				testDigestKey:            launcher.SelectedSHA256,
			}
			require.Equal(t, Field[string]{Value: tc.want, Raw: tc.raw, Status: tc.wantStatus, Source: tc.key}, fields[tc.key])
		})
	}
}

func TestCgroupVersionAndGC(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, version, gc       string
		versionStatus, gcStatus Status
		wantVersion, wantGC     int64
	}{
		{"v1 GC off", "1", "off", StatusPresent, StatusPresent, 1, -1},
		{"v2 GC zero", "2", "0", StatusPresent, StatusPresent, 2, 0},
		{"unknown version", "0", "-1", StatusUnsupported, StatusInvalid, 0, 0},
		{"future version", "3", "OFF", StatusUnsupported, StatusInvalid, 0, 0},
		{"malformed version", "invalid", "", StatusInvalid, StatusInvalid, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			info := readLauncher(lookupMap(map[string]string{
				"MICROFAT_CGROUP_VERSION": tc.version, "MICROFAT_CGROUP_GOGC": tc.gc,
			})).Cgroup
			assert.Equal(t, Field[int64]{Value: tc.wantVersion, Raw: tc.version,
				Status: tc.versionStatus, Source: "MICROFAT_CGROUP_VERSION"}, info.Version)
			assert.Equal(t, Field[int64]{Value: tc.wantGC, Raw: tc.gc,
				Status: tc.gcStatus, Source: "MICROFAT_CGROUP_GOGC"}, info.RecommendedGOGC)
		})
	}
}

func TestFiniteZeroAndUnappliedRecommendations(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"MICROFAT_CGROUP_LIMIT_BYTES": "0", "MICROFAT_CGROUP_HIGH_BYTES": "0",
		"MICROFAT_CGROUP_EFFECTIVE_LIMIT_BYTES": "0", "MICROFAT_CGROUP_CPUS": "0.00",
		"MICROFAT_CGROUP_GOMEMLIMIT": "0B", "MICROFAT_CGROUP_GOMAXPROCS": "4", "MICROFAT_CGROUP_GOGC": "off",
		"GOMEMLIMIT": "off", "GOMAXPROCS": "1", "GOGC": "100", "MICROFAT_AUTOTUNE": "0", "MICROFAT_DRY_RUN": "1",
	}
	launcher, configured := readLauncher(lookupMap(values)), readConfigured(lookupMap(values))
	for _, field := range []Field[int64]{launcher.Cgroup.MemoryLimitBytes, launcher.Cgroup.MemoryHighBytes,
		launcher.Cgroup.EffectiveMemoryLimitBytes} {
		assert.Equal(t, StatusPresent, field.Status, "zero is preserved without inventing unlimited or unavailable semantics")
		assert.Zero(t, field.Value)
	}
	assert.Zero(t, launcher.Cgroup.CPUQuota.Value)
	assert.Equal(t, "0.00", launcher.Cgroup.CPUQuota.Raw)
	assert.Equal(t, "0B", launcher.Cgroup.RecommendedGOMEMLIMIT.Value)
	assert.Equal(t, "off", configured.GOMEMLIMIT.Value)
	assert.Equal(t, int64(4), launcher.Cgroup.RecommendedGOMAXPROCS.Value)
	assert.Equal(t, "1", configured.GOMAXPROCS.Value)
	assert.Equal(t, int64(-1), launcher.Cgroup.RecommendedGOGC.Value)
	assert.Equal(t, "100", configured.GOGC.Value)
	assert.Equal(t, "0", configured.Autotune.Value)
	assert.Equal(t, "1", configured.DryRun.Value)
	delete(values, "MICROFAT_CGROUP_HIGH_BYTES")
	assert.Equal(t, StatusMissing, readLauncher(lookupMap(values)).Cgroup.MemoryHighBytes.Status)
}

func TestReadsReflectCurrentLookup(t *testing.T) {
	t.Parallel()
	values := map[string]string{"MICROFAT_SELECTED_VARIANT": "future-level", "GOMEMLIMIT": "512MiB"}
	lookup := lookupMap(values)
	firstLauncher, firstConfigured := readLauncher(lookup), readConfigured(lookup)
	values["MICROFAT_SELECTED_VARIANT"] = "v1"
	values["GOMEMLIMIT"] = ""
	values["MICROFAT_FUTURE_FIELD"] = "ignored"
	secondLauncher, secondConfigured := readLauncher(lookup), readConfigured(lookup)
	assert.Equal(t, "future-level", firstLauncher.SelectedVariant.Value)
	assert.Equal(t, "512MiB", firstConfigured.GOMEMLIMIT.Value)
	assert.Equal(t, "v1", secondLauncher.SelectedVariant.Value)
	assert.Equal(t, StatusPresent, secondConfigured.GOMEMLIMIT.Status)
	assert.Empty(t, secondConfigured.GOMEMLIMIT.Value)
	delete(values, "GOMEMLIMIT")
	assert.Equal(t, StatusMissing, readConfigured(lookup).GOMEMLIMIT.Status)
}

func TestObserveUnsupportedMetrics(t *testing.T) {
	t.Parallel()
	samples := []metrics.Sample{{Name: "/microfat/unknown:count"}, {Name: "/cpu/classes/total:cpu-seconds"}}
	metrics.Read(samples)
	for _, sample := range samples {
		t.Run(sample.Name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, Field[uint64]{Status: StatusUnavailable, Source: "runtime/metrics:" + sample.Name}, observe(sample))
		})
	}
}

func TestReadObservesCurrentRuntimeWithoutMutation(t *testing.T) {
	t.Parallel()
	samples := []metrics.Sample{{Name: metricMemoryLimit}, {Name: metricMaxProcs}, {Name: metricGCPercent}}
	metrics.Read(samples)
	before := [3]uint64{samples[0].Value.Uint64(), samples[1].Value.Uint64(), samples[2].Value.Uint64()}
	snapshot := Read()
	metrics.Read(samples)
	for index, field := range []Field[uint64]{snapshot.Observed.GOMEMLIMITBytes,
		snapshot.Observed.GOMAXPROCS, snapshot.Observed.GOGCPercent} {
		assert.Equal(t, StatusPresent, field.Status)
		assert.Empty(t, field.Raw)
		assert.Equal(t, "runtime/metrics:"+samples[index].Name, field.Source)
		// Native CPU policy may update GOMAXPROCS concurrently with a snapshot.
		if index == 1 {
			assert.Positive(t, field.Value)
			continue
		}
		assert.Equal(t, before[index], field.Value)
		assert.Equal(t, before[index], samples[index].Value.Uint64())
	}
}

func TestCoreDependsOnlyOnStandardLibrary(t *testing.T) {
	t.Parallel()
	// Listing production dependencies excludes the test assertion library and SDK recipes.
	command := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Equal(t, "github.com/EpicBlackWolfZ/microfat/runtimeinfo", strings.TrimSpace(string(output)))
}

func TestParsePreservesRawOverflow(t *testing.T) {
	t.Parallel()
	values := map[string]string{"MICROFAT_SELECTED_SIZE": strconv.FormatUint(math.MaxUint64, decimalBase)}
	launcher := readLauncher(lookupMap(values))
	assert.Equal(t, StatusInvalid, launcher.SelectedSizeBytes.Status)
	assert.Equal(t, values["MICROFAT_SELECTED_SIZE"], launcher.SelectedSizeBytes.Raw)
	assert.Zero(t, launcher.SelectedSizeBytes.Value)
}
