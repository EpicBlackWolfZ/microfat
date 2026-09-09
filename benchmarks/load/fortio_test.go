package load

import (
	"context"
	json "encoding/json/v2"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/workloads/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testOptions() Options {
	return Options{Duration: time.Second, QPS: 1000, Concurrency: 1, Resolution: defaultResolution,
		RawPath: "trials/fixture/fortio.json"}
}

func TestPinnedOutputParser(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/fortio-1.75.2.json")
	require.NoError(t, err)
	result, err := Parse(data, testOptions())
	require.NoError(t, err)
	assert.Positive(t, result.Requests)
	assert.Equal(t, result.Requests, result.Successful)
	assert.Len(t, result.PercentilesSeconds, 6)
	assert.Contains(t, result.CoordinatedOmission, "uncorrected")
	var raw fortioJSON
	require.NoError(t, json.Unmarshal(data, &raw))
	for name, mutate := range map[string]func(*fortioJSON){
		"version":            func(r *fortioJSON) { r.Version = "future" },
		"missing percentile": func(r *fortioJSON) { r.DurationHistogram.Percentiles = nil },
		"duplicate percentile": func(r *fortioJSON) {
			r.DurationHistogram.Percentiles = append(r.DurationHistogram.Percentiles, r.DurationHistogram.Percentiles[0])
		},
		"count mismatch": func(r *fortioJSON) { r.DurationHistogram.Count++ },
	} {
		t.Run(name, func(t *testing.T) {
			var fixture fortioJSON
			require.NoError(t, json.Unmarshal(data, &fixture))
			mutate(&fixture)
			encoded, err := json.Marshal(fixture)
			require.NoError(t, err)
			_, err = Parse(encoded, testOptions())
			require.Error(t, err)
		})
	}
	raw.RetCodes["500"], raw.RetCodes["200"] = raw.RetCodes["200"], 0
	raw.ActualQPS = 1
	encoded, err := json.Marshal(raw)
	require.NoError(t, err)
	result, err = Parse(encoded, testOptions())
	require.NoError(t, err)
	assert.Contains(t, result.Warnings, "HTTP or transport failures")
	assert.Contains(t, result.Warnings, "target QPS not achieved")
	_, err = Parse([]byte("{"), testOptions())
	require.Error(t, err)
	_, err = Parse(make([]byte, process.MaxOutput+1), testOptions())
	require.ErrorIs(t, err, process.ErrOutputLimit)
	_, err = Parse(data, Options{})
	require.Error(t, err)
}

func fakeTool(t *testing.T, version, action string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "fortio")
	content := "#!/bin/sh\nif [ \"$1\" = version ]; then printf '%s\\n' '" + version + "'; exit; fi\n" + action + "\n"
	require.NoError(t, os.WriteFile(file, []byte(content), 0o755))
	return file
}

func TestExternalDriver(t *testing.T) {
	t.Parallel()
	fixture, err := filepath.Abs("testdata/fortio-1.75.2.json")
	require.NoError(t, err)
	driver := &Fortio{Path: fakeTool(t, Version, "cat '"+fixture+"'"),
		Wrap: func(spec process.Spec) process.Spec { return spec }}
	ctx, cancel := context.WithTimeout(context.Background(), loadGrace)
	defer cancel()
	require.NoError(t, driver.CheckVersion(ctx))
	options := testOptions()
	options.Resolution = 0
	require.NoError(t, driver.Configure(options))
	result, err := driver.Run(ctx, "http://127.0.0.1:1/mixed")
	require.NoError(t, err)
	assert.Positive(t, result.Process.PID)
	assert.NotEqual(t, os.Getpid(), result.Process.PID)
	assert.Contains(t, result.Process.Arguments, "-nocatchup")
	require.NoError(t, driver.Stop(ctx))
	require.Error(t, driver.Configure(Options{}))
	_, err = driver.Run(ctx, "https://example.test")
	require.Error(t, err)
	driver.running = true
	require.Error(t, driver.Configure(options))
	_, err = driver.Run(ctx, "http://127.0.0.1:1")
	require.Error(t, err)
	driver.running = false
	driver.Path = fakeTool(t, "wrong", "exit 1")
	require.Error(t, driver.CheckVersion(ctx))
	result, err = driver.Run(ctx, "http://127.0.0.1:1")
	require.Error(t, err)
	require.NotNil(t, result)
	driver.Path = "/missing"
	require.Error(t, driver.CheckVersion(ctx))
	_, err = driver.Run(ctx, "http://127.0.0.1:1")
	require.Error(t, err)
	driver.options = Options{}
	_, err = driver.Run(ctx, "http://127.0.0.1:1")
	require.Error(t, err)
}

func TestDriverCancellation(t *testing.T) {
	t.Parallel()
	driver := &Fortio{Path: fakeTool(t, Version, "sleep 60")}
	require.NoError(t, driver.Configure(testOptions()))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := driver.Run(ctx, "http://127.0.0.1:1"); done <- err }()
	require.Eventually(t, func() bool {
		driver.mu.Lock()
		defer driver.mu.Unlock()
		return driver.child != nil
	}, time.Second, time.Millisecond)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	require.Error(t, driver.Stop(stopCtx))
	cancel()
	require.Error(t, <-done)
}

func FuzzFortioJSON(f *testing.F) {
	data, err := os.ReadFile("testdata/fortio-1.75.2.json")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(data)
	f.Add([]byte("{}"))
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > process.MaxOutput {
			t.Skip()
		}
		_, _ = Parse(input, testOptions())
	})
}

func TestRealFortioIntegration(t *testing.T) {
	path := os.Getenv("MICROFAT_BENCH_FORTIO")
	if path == "" {
		t.Skip("set MICROFAT_BENCH_FORTIO for pinned-tool integration")
	}
	driver := &Fortio{Path: path}
	ctx, cancel := context.WithTimeout(context.Background(), loadGrace)
	defer cancel()
	require.NoError(t, driver.CheckVersion(ctx))
	handler, err := server.Handler(server.Defaults())
	require.NoError(t, err)
	target := httptest.NewServer(handler)
	defer target.Close()
	for _, endpoint := range []string{"mixed", "cpu", "memory", "missing"} {
		t.Run(endpoint, func(t *testing.T) {
			options := testOptions()
			options.Duration = 200 * time.Millisecond
			require.NoError(t, driver.Configure(options))
			result, err := driver.Run(ctx, target.URL+"/"+endpoint)
			require.NoError(t, err)
			require.Positive(t, result.HTTP.Requests)
			if endpoint == "missing" {
				assert.Zero(t, result.HTTP.Successful)
				assert.Positive(t, result.HTTP.StatusCodes["404"])
			} else {
				assert.Equal(t, result.HTTP.Requests, result.HTTP.Successful)
			}
			assert.NotEqual(t, os.Getpid(), result.Process.PID)
			assert.NotEmpty(t, result.Resources)
		})
	}
	target.Close()
	result, err := driver.Run(ctx, target.URL+"/mixed")
	// Fortio may reject an unreachable target during its preflight before emitting a histogram.
	if err == nil {
		require.Zero(t, result.HTTP.Successful)
	} else {
		require.NotNil(t, result)
		require.NotEmpty(t, result.Stderr)
	}
}
