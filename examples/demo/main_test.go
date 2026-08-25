package main

import (
	"bytes"
	"testing"
)

func TestDemoWorkloads(t *testing.T) {
	// Test Phase A
	mA := runSIMDMathWorkload()
	if mA.Operations == 0 || mA.ComputeMs <= 0 {
		t.Errorf("Phase A returned invalid metrics: %+v", mA)
	}

	// Test Phase B
	mB := runJSONMemoryWorkload()
	if mB.Operations == 0 || mB.ComputeMs <= 0 {
		t.Errorf("Phase B returned invalid metrics: %+v", mB)
	}

	// Test Phase C
	mC := runConcurrentWorkload()
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
}
