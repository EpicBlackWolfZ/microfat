package testutil

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckGoroutineLeaks_Clean(t *testing.T) {
	AssertNoGoroutineLeaks(t)
}

func TestHelperProcessLeak(t *testing.T) {
	if os.Getenv("SUBPROCESS_LEAK") != "1" {
		return
	}
	// Intentionally leak a permanently blocked goroutine
	go func() {
		select {}
	}()
	time.Sleep(50 * time.Millisecond)

	var buf bytes.Buffer
	err := CheckGoroutineLeaks(&buf)
	if err == nil {
		t.Fatalf("expected leak to be detected, but got nil")
	}
	if !strings.Contains(err.Error(), "detected 1 leaked goroutine(s)") {
		t.Fatalf("unexpected error message: %v", err)
	}
	os.Exit(0)
}

func TestCheckGoroutineLeaks_DetectsLeak(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcessLeak$")
	cmd.Env = append(os.Environ(), "SUBPROCESS_LEAK=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "subprocess failed: %s", string(out))
}

func TestHelperProcessAssertFails(t *testing.T) {
	if os.Getenv("SUBPROCESS_ASSERT_FAIL") != "1" {
		return
	}
	go func() {
		select {}
	}()
	time.Sleep(50 * time.Millisecond)
	AssertNoGoroutineLeaks(t)
}

func TestAssertNoGoroutineLeaks_FailsOnLeak(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcessAssertFails$")
	cmd.Env = append(os.Environ(), "SUBPROCESS_ASSERT_FAIL=1")
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "expected subprocess to fail due to leaked goroutine")
	assert.Contains(t, string(out), "goroutine leak check failed: detected 1 leaked goroutine(s)")
}

func TestCheckLeaks_NonZeroExit(t *testing.T) {
	code := checkLeaks(func() int { return 42 })
	assert.Equal(t, 42, code)
}

func TestCheckLeaks_CleanWhenEnabled(t *testing.T) {
	t.Setenv("MICROFAT_TEST_LEAKS", "1")
	code := checkLeaks(func() int { return 0 })
	assert.Equal(t, 0, code)
}

func TestHelperProcessCheckLeaksDetects(t *testing.T) {
	if os.Getenv("SUBPROCESS_CHECK_LEAKS_FAIL") != "1" {
		return
	}
	os.Setenv("MICROFAT_TEST_LEAKS", "1")
	go func() {
		select {}
	}()
	time.Sleep(50 * time.Millisecond)
	code := checkLeaks(func() int { return 0 })
	os.Exit(code)
}

func TestCheckLeaks_DetectsLeakInRunner(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcessCheckLeaksDetects$")
	cmd.Env = append(os.Environ(), "SUBPROCESS_CHECK_LEAKS_FAIL=1")
	out, err := cmd.CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(out), "[microfat:leak-check]")
}
