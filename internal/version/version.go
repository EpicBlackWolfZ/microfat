// Package version provides runtime and build-time metadata for microfat binaries.
package version

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
)

// Injected via -ldflags at build time by GoReleaser or Makefile.
var (
	Role    = "standalone"
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
	BuiltBy = "local"
	Vendor  = "EpicBlackWolfZ"
	OS      = runtime.GOOS
	Arch    = runtime.GOARCH
)

// ANSI color escape codes for terminal styling.
const (
	ColorReset  = "\033[0m"
	ColorCyan   = "\033[36m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBold   = "\033[1m"
)

// CustomField allows applications to add custom metadata lines to the startup banner.
type CustomField struct {
	Key   string
	Value string
}

// Info returns a formatted version and build details string.
func Info() string {
	return fmt.Sprintf("Version: %s (Commit: %s, Built: %s, BuiltBy: %s, Go: %s, %s/%s)",
		Version, Commit, Date, BuiltBy, runtime.Version(), OS, Arch)
}

// Short returns a concise version string, appending commit and date if available.
func Short() string {
	s := Version
	if Commit != "" && Commit != "none" {
		s += " (" + Commit + ")"
	}
	if Date != "" && Date != "unknown" {
		s += " built " + Date
	}
	return s
}

// FprintStartupBanner renders an ASCII art logo and build metadata to the specified writer.
func FprintStartupBanner(w io.Writer, appName, asciiLogo string, customFields ...CustomField) {
	if asciiLogo != "" {
		_, _ = fmt.Fprintf(w, "%s%s%s\n", ColorCyan, asciiLogo, ColorReset)
	} else if appName != "" {
		_, _ = fmt.Fprintf(w, "%s%s=== %s ===%s\n", ColorBold, ColorCyan, appName, ColorReset)
	}

	_, _ = fmt.Fprintf(w, "  %-12s: %s%s%s\n", "Role", ColorYellow, Role, ColorReset)
	_, _ = fmt.Fprintf(w, "  %-12s: %s%s%s\n", "Version", ColorGreen, Version, ColorReset)
	_, _ = fmt.Fprintf(w, "  %-12s: %s\n", "Commit", Commit)
	_, _ = fmt.Fprintf(w, "  %-12s: %s\n", "Build Date", Date)
	_, _ = fmt.Fprintf(w, "  %-12s: %s\n", "Built By", BuiltBy)
	_, _ = fmt.Fprintf(w, "  %-12s: %s\n", "Vendor", Vendor)
	_, _ = fmt.Fprintf(w, "  %-12s: %s/%s\n", "Platform", OS, Arch)
	_, _ = fmt.Fprintf(w, "  %-12s: %s\n", "Go Version", runtime.Version())

	for _, f := range customFields {
		_, _ = fmt.Fprintf(w, "  %-12s: %s\n", f.Key, f.Value)
	}
	_, _ = fmt.Fprintln(w)
}

// PrintStartupBanner renders an ASCII art logo and build metadata to standard output.
func PrintStartupBanner(appName, asciiLogo string, customFields ...CustomField) {
	FprintStartupBanner(os.Stdout, appName, asciiLogo, customFields...)
}

// RegisterFlags registers --version and -v flags to the provided FlagSet.
func RegisterFlags(fs *flag.FlagSet) *bool {
	var showVersion bool
	fs.BoolVar(&showVersion, "version", false, "Print version information and exit")
	fs.BoolVar(&showVersion, "v", false, "Print version information (shorthand)")
	return &showVersion
}
