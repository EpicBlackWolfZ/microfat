package testutil

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"strings"
	"testing"
)

// CheckGoroutineLeaks inspects the Go 1.27 runtime "goroutineleak" profile.
// If any leaked goroutines are detected, it writes the leak profile to w (if w != nil)
// and returns an error describing the leak count.
func CheckGoroutineLeaks(w io.Writer) error {
	p := pprof.Lookup("goroutineleak")
	if p == nil {
		return nil
	}
	var buf bytes.Buffer
	if err := p.WriteTo(&buf, 1); err != nil {
		return fmt.Errorf("reading goroutineleak profile: %w", err)
	}
	if p.Count() == 0 {
		return nil
	}
	if w != nil {
		_, _ = w.Write(buf.Bytes())
	}
	return fmt.Errorf("detected %d leaked goroutine(s):\n%s", p.Count(), buf.String())
}

// AssertNoGoroutineLeaks asserts that no goroutines have leaked according to the
// Go 1.27 goroutineleak profiler.
func AssertNoGoroutineLeaks(t testing.TB) {
	t.Helper()
	var buf bytes.Buffer
	if err := CheckGoroutineLeaks(&buf); err != nil {
		t.Fatalf("goroutine leak check failed: %v", err)
	}
}

// CheckLeaksIfEnabled runs the test suite and, if MICROFAT_TEST_LEAKS is enabled,
// checks for goroutine leaks before process exit.
func CheckLeaksIfEnabled(m *testing.M) int {
	code := m.Run()
	if code != 0 {
		return code
	}
	env := strings.TrimSpace(os.Getenv("MICROFAT_TEST_LEAKS"))
	if env == "1" || strings.EqualFold(env, "true") {
		var buf bytes.Buffer
		if err := CheckGoroutineLeaks(&buf); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "[microfat:leak-check] %v\n", err)
			return 1
		}
	}
	return code
}
