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

	// Test heavy mode
	mHeavy := runSIMDMathWorkload(LevelHeavy)
	if mHeavy.Operations == 0 || mHeavy.ComputeMs <= 0 {
		t.Errorf("Phase A heavy returned invalid metrics: %+v", mHeavy)
	}

	// Test ultra mode
	mUltra := runSIMDMathWorkload(LevelUltra)
	if mUltra.Operations == 0 || mUltra.ComputeMs <= 0 {
		t.Errorf("Phase A ultra returned invalid metrics: %+v", mUltra)
	}

	// Test CLI commands
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("root help failed: %v", err)
	}
}
