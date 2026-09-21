package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDemoWorkloads(t *testing.T) {
	// Test Phase A (Standard and SIMD modes)
	mA := runSIMDMathWorkload(LevelStandard, false)
	assert.NotZero(t, mA.Operations)
	assert.Positive(t, mA.ComputeMs)
	assert.NotEmpty(t, mA.Detail)

	mASIMD := runSIMDMathWorkload(LevelStandard, true)
	assert.NotZero(t, mASIMD.Operations)
	assert.Positive(t, mASIMD.ComputeMs)
	assert.Contains(t, mASIMD.Detail, "SIMD 8-Way Unrolled")

	// Test Phase A with Heavy and Ultra levels in SIMD mode
	mAHeavy := runSIMDMathWorkload(LevelHeavy, true)
	assert.NotZero(t, mAHeavy.Operations)
	assert.Positive(t, mAHeavy.ComputeMs)

	// Test Phase B across levels
	mB := runJSONMemoryWorkload(LevelStandard)
	assert.NotZero(t, mB.Operations)
	assert.Positive(t, mB.ComputeMs)

	mBHeavy := runJSONMemoryWorkload(LevelHeavy)
	assert.NotZero(t, mBHeavy.Operations)
	assert.Positive(t, mBHeavy.ComputeMs)

	// Test Phase C across levels
	mC := runConcurrentWorkload(LevelStandard)
	assert.NotZero(t, mC.Operations)
	assert.Positive(t, mC.ComputeMs)

	mCHeavy := runConcurrentWorkload(LevelHeavy)
	assert.NotZero(t, mCHeavy.Operations)
	assert.Positive(t, mCHeavy.ComputeMs)

	// Test CLI commands
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.Execute(), "root help failed")

	// Test all subcommands via root
	r1 := newRootCmd()
	r1.SetArgs([]string{"math", "--json", "--simd"})
	require.NoError(t, r1.Execute(), "math command with simd failed")

	r2 := newRootCmd()
	r2.SetArgs([]string{"json-mem"})
	require.NoError(t, r2.Execute(), "json command failed")

	r3 := newRootCmd()
	r3.SetArgs([]string{"concurrent", "--json"})
	require.NoError(t, r3.Execute(), "concurrent command failed")

	r4 := newRootCmd()
	r4.SetArgs([]string{"all", "--heavy", "--simd"})
	require.NoError(t, r4.Execute(), "all command with heavy and simd failed")

	r5 := newRootCmd()
	r5.SetArgs([]string{"all", "--json", "--simd"})
	require.NoError(t, r5.Execute(), "all command with json failed")

	// Test --cpu-profile
	tempDir := t.TempDir()
	profPath := filepath.Join(tempDir, "test_cpu.pprof")
	rProf := newRootCmd()
	rProf.SetArgs([]string{"all", "--cpu-profile", profPath})
	require.NoError(t, rProf.Execute(), "all command with cpu-profile failed")

	if st, err := os.Stat(profPath); err != nil || st.Size() == 0 {
		t.Errorf("expected non-empty cpu profile file at %s: %v", profPath, err)
	}

	// Test --cpu-profile error path
	rProfErr := newRootCmd()
	rProfErr.SetArgs([]string{"all", "--cpu-profile", "/dev/null/forbidden/prof.pprof"})
	if err := rProfErr.Execute(); err == nil {
		t.Errorf("expected error on invalid cpu profile path")
	}

	// Test --startup-only
	rStartup := newRootCmd()
	var startupBuf bytes.Buffer
	rStartup.SetOut(&startupBuf)
	rStartup.SetArgs([]string{"--startup-only"})
	require.NoError(t, rStartup.Execute(), "startup-only failed")
	if startupBuf.String() != "READY\n" {
		t.Errorf("expected 'READY\\n', got %q", startupBuf.String())
	}

	// Test --startup-only with --json
	rStartupJSON := newRootCmd()
	var startupJSONBuf bytes.Buffer
	rStartupJSON.SetOut(&startupJSONBuf)
	rStartupJSON.SetArgs([]string{"--startup-only", "--json"})
	require.NoError(t, rStartupJSON.Execute(), "startup-only json failed")
	if !bytes.Contains(startupJSONBuf.Bytes(), []byte(`"status": "ready"`)) {
		t.Errorf("expected ready json, got %q", startupJSONBuf.String())
	}

	// Test getWorkloadLevel, levelToString, and fallbackStr
	flagUltra = false
	flagHeavy = false
	if getWorkloadLevel() != LevelStandard || levelToString(LevelStandard) != "standard" {
		t.Errorf("expected LevelStandard")
	}
	flagHeavy = true
	if getWorkloadLevel() != LevelHeavy || levelToString(LevelHeavy) != "heavy" {
		t.Errorf("expected LevelHeavy")
	}
	flagUltra = true
	if getWorkloadLevel() != LevelUltra || levelToString(LevelUltra) != "ultra" {
		t.Errorf("expected LevelUltra")
	}
	flagUltra = false
	flagHeavy = false

	if fallbackStr("", "default") != "default" || fallbackStr("custom", "default") != "custom" {
		t.Errorf("fallbackStr failed")
	}

	// Test concurrent pool reuse under race detector
	var poolWg sync.WaitGroup
	for range 8 {
		poolWg.Go(func() {
			_ = runJSONMemoryWorkload(LevelStandard)
			_ = runConcurrentWorkload(LevelStandard)
		})
	}
	poolWg.Wait()

	// Test main() with --help
	main()
}
