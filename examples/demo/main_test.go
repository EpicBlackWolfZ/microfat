package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDemoWorkloads(t *testing.T) {
	// Test Phase A (Standard and SIMD modes)
	mA := runSIMDMathWorkload(LevelStandard, false)
	if mA.Operations == 0 || mA.ComputeMs <= 0 || mA.Detail == "" {
		t.Errorf("Phase A standard returned invalid metrics: %+v", mA)
	}

	mASIMD := runSIMDMathWorkload(LevelStandard, true)
	if mASIMD.Operations == 0 || mASIMD.ComputeMs <= 0 || !bytes.Contains([]byte(mASIMD.Detail), []byte("SIMD 8-Way Unrolled")) {
		t.Errorf("Phase A SIMD returned invalid metrics: %+v", mASIMD)
	}

	// Test Phase A with Heavy and Ultra levels in SIMD mode
	mAHeavy := runSIMDMathWorkload(LevelHeavy, true)
	if mAHeavy.Operations == 0 || mAHeavy.ComputeMs <= 0 {
		t.Errorf("Phase A heavy SIMD returned invalid metrics: %+v", mAHeavy)
	}

	// Test Phase B across levels
	mB := runJSONMemoryWorkload(LevelStandard)
	if mB.Operations == 0 || mB.ComputeMs <= 0 {
		t.Errorf("Phase B returned invalid metrics: %+v", mB)
	}

	mBHeavy := runJSONMemoryWorkload(LevelHeavy)
	if mBHeavy.Operations == 0 || mBHeavy.ComputeMs <= 0 {
		t.Errorf("Phase B heavy returned invalid metrics: %+v", mBHeavy)
	}

	// Test Phase C across levels
	mC := runConcurrentWorkload(LevelStandard)
	if mC.Operations == 0 || mC.ComputeMs <= 0 {
		t.Errorf("Phase C returned invalid metrics: %+v", mC)
	}

	mCHeavy := runConcurrentWorkload(LevelHeavy)
	if mCHeavy.Operations == 0 || mCHeavy.ComputeMs <= 0 {
		t.Errorf("Phase C heavy returned invalid metrics: %+v", mCHeavy)
	}

	// Test CLI commands
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("root help failed: %v", err)
	}

	// Test all subcommands via root
	r1 := newRootCmd()
	r1.SetArgs([]string{"math", "--json", "--simd"})
	if err := r1.Execute(); err != nil {
		t.Fatalf("math command with simd failed: %v", err)
	}

	r2 := newRootCmd()
	r2.SetArgs([]string{"json-mem"})
	if err := r2.Execute(); err != nil {
		t.Fatalf("json command failed: %v", err)
	}

	r3 := newRootCmd()
	r3.SetArgs([]string{"concurrent", "--json"})
	if err := r3.Execute(); err != nil {
		t.Fatalf("concurrent command failed: %v", err)
	}

	r4 := newRootCmd()
	r4.SetArgs([]string{"all", "--heavy", "--simd"})
	if err := r4.Execute(); err != nil {
		t.Fatalf("all command with heavy and simd failed: %v", err)
	}

	r5 := newRootCmd()
	r5.SetArgs([]string{"all", "--json", "--simd"})
	if err := r5.Execute(); err != nil {
		t.Fatalf("all command with json failed: %v", err)
	}

	// Test --cpu-profile
	tempDir := t.TempDir()
	profPath := filepath.Join(tempDir, "test_cpu.pprof")
	rProf := newRootCmd()
	rProf.SetArgs([]string{"all", "--cpu-profile", profPath})
	if err := rProf.Execute(); err != nil {
		t.Fatalf("all command with cpu-profile failed: %v", err)
	}

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
	if err := rStartup.Execute(); err != nil {
		t.Fatalf("startup-only failed: %v", err)
	}
	if startupBuf.String() != "READY\n" {
		t.Errorf("expected 'READY\\n', got %q", startupBuf.String())
	}

	// Test --startup-only with --json
	rStartupJSON := newRootCmd()
	var startupJSONBuf bytes.Buffer
	rStartupJSON.SetOut(&startupJSONBuf)
	rStartupJSON.SetArgs([]string{"--startup-only", "--json"})
	if err := rStartupJSON.Execute(); err != nil {
		t.Fatalf("startup-only json failed: %v", err)
	}
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
	for i := 0; i < 8; i++ {
		poolWg.Add(1)
		go func() {
			defer poolWg.Done()
			_ = runJSONMemoryWorkload(LevelStandard)
			_ = runConcurrentWorkload(LevelStandard)
		}()
	}
	poolWg.Wait()

	// Test main() with --help
	main()
}
