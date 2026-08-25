package main

import (
	"bytes"
	"testing"
)

func TestRootCmdAndSubcommands(t *testing.T) {
	root := newRootCmd()
	if root == nil {
		t.Fatalf("expected non-nil root command")
	}

	// Test detect command
	detect := newDetectCmd()
	detect.SetArgs([]string{"--json"})
	var buf bytes.Buffer
	detect.SetOut(&buf)
	if err := detect.Execute(); err != nil {
		t.Fatalf("detect command failed: %v", err)
	}

	// Test inspect validation
	inspect := newInspectCmd()
	inspect.SetArgs([]string{"/non/existent/binary/path"})
	if err := inspect.Execute(); err == nil {
		t.Errorf("expected inspect on non-existent binary to fail")
	}

	// Test verify validation
	verify := newVerifyCmd()
	verify.SetArgs([]string{"/non/existent/binary/path"})
	if err := verify.Execute(); err == nil {
		t.Errorf("expected verify on non-existent binary to fail")
	}

	// Test trim validation
	trim := newTrimCmd()
	trim.SetArgs([]string{"/non/existent/binary/path"})
	if err := trim.Execute(); err == nil {
		t.Errorf("expected trim on non-existent binary to fail")
	}

	// Test pack validation errors
	pack := newPackCmd()
	pack.SetArgs([]string{"--stub", "", "--output", "", "--variant", "invalid"})
	if err := pack.Execute(); err == nil {
		t.Errorf("expected pack with invalid args to fail")
	}
}
