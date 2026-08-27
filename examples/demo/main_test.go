package main

import (
	"bytes"
	"testing"
)

func TestDemoWorkloads(t *testing.T) {
	// Test Phase A
	mA := runSIMDMathWorkload(LevelStandard)
	if mA.Operations == 0 || mA.ComputeMs <= 0 {
		t.Errorf("Phase A returned invalid metrics: %+v", mA)
	}

	// Test Phase B
	mB := runJSONMemoryWorkload(LevelStandard)
	if mB.Operations == 0 || mB.ComputeMs <= 0 {
		t.Errorf("Phase B returned invalid metrics: %+v", mB)
	}

	// Test Phase C
	mC := runConcurrentWorkload(LevelStandard)
	if mC.Operations == 0 || mC.ComputeMs <= 0 {
		t.Errorf("Phase C returned invalid metrics: %+v", mC)
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
	r1.SetArgs([]string{"math", "--json"})
	if err := r1.Execute(); err != nil {
		t.Fatalf("math command failed: %v", err)
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
	r4.SetArgs([]string{"all", "--heavy"})
	if err := r4.Execute(); err != nil {
		t.Fatalf("all command with heavy failed: %v", err)
	}

	r5 := newRootCmd()
	r5.SetArgs([]string{"all", "--json"})
	if err := r5.Execute(); err != nil {
		t.Fatalf("all command with json failed: %v", err)
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

	// Test getWorkloadLevel and fallbackStr
	flagUltra = false
	flagHeavy = false
	if getWorkloadLevel() != LevelStandard {
		t.Errorf("expected LevelStandard")
	}
	flagHeavy = true
	if getWorkloadLevel() != LevelHeavy {
		t.Errorf("expected LevelHeavy")
	}
	flagUltra = true
	if getWorkloadLevel() != LevelUltra {
		t.Errorf("expected LevelUltra")
	}
	flagUltra = false
	flagHeavy = false

	if fallbackStr("", "default") != "default" || fallbackStr("custom", "default") != "custom" {
		t.Errorf("fallbackStr failed")
	}

	// Test main() with --help
	main()
}
