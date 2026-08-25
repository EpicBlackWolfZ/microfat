package version

import (
	"bytes"
	"flag"
	"runtime"
	"strings"
	"testing"
)

func TestInfo(t *testing.T) {
	Version = "1.2.3"
	Commit = "abcdef0"
	Date = "2026-08-20T12:00:00Z"
	BuiltBy = "test-runner"
	Vendor = "EpicBlackWolfZ"

	info := Info()
	if !strings.Contains(info, "Version: 1.2.3") {
		t.Errorf("expected Version 1.2.3 in Info, got: %s", info)
	}
	if !strings.Contains(info, "Commit: abcdef0") {
		t.Errorf("expected Commit in Info, got: %s", info)
	}
	if !strings.Contains(info, runtime.Version()) {
		t.Errorf("expected Go version in Info, got: %s", info)
	}
}

func TestShort(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		commit   string
		date     string
		expected string
	}{
		{
			name:     "clean dev",
			version:  "dev",
			commit:   "none",
			date:     "unknown",
			expected: "dev",
		},
		{
			name:     "with commit and date",
			version:  "1.0.0",
			commit:   "1234567",
			date:     "2026-08-20",
			expected: "1.0.0 (1234567) built 2026-08-20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.version
			Commit = tt.commit
			Date = tt.date

			got := Short()
			if got != tt.expected {
				t.Errorf("Short() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestFprintStartupBanner(t *testing.T) {
	var buf bytes.Buffer
	FprintStartupBanner(&buf, "test-app", "=== BANNER ===", CustomField{Key: "Custom", Value: "Value123"})
	output := buf.String()

	if !strings.Contains(output, "BANNER") {
		t.Errorf("expected banner in output, got: %s", output)
	}
	if !strings.Contains(output, "Custom") || !strings.Contains(output, "Value123") {
		t.Errorf("expected custom field in output, got: %s", output)
	}

	var buf2 bytes.Buffer
	FprintStartupBanner(&buf2, "test-app-no-logo", "")
	output2 := buf2.String()
	if !strings.Contains(output2, "=== test-app-no-logo ===") {
		t.Errorf("expected default header in output, got: %s", output2)
	}

	// Also call PrintStartupBanner for complete coverage
	PrintStartupBanner("test-stdout", "", CustomField{Key: "K", Value: "V"})
}

func TestRegisterFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	showVersion := RegisterFlags(fs)

	err := fs.Parse([]string{"--version"})
	if err != nil {
		t.Fatalf("failed to parse flags: %v", err)
	}
	if !*showVersion {
		t.Errorf("expected showVersion to be true after --version")
	}

	fs2 := flag.NewFlagSet("test2", flag.ContinueOnError)
	showVersion2 := RegisterFlags(fs2)
	err = fs2.Parse([]string{"-v"})
	if err != nil {
		t.Fatalf("failed to parse flags: %v", err)
	}
	if !*showVersion2 {
		t.Errorf("expected showVersion2 to be true after -v")
	}
}
