// Package docgate validates repository documentation, ADRs, normative SPECs,
// the formal verification ledger, and code-to-documentation parity.
package docgate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxDocFileBytes   = 8 << 20
	submatchOneGroup  = 2
	submatchTwoGroups = 3
)

// Finding describes a single documentation or governance validation failure.
type Finding struct {
	Path    string
	Line    int
	Rule    string
	Message string
}

// Report aggregates all findings produced by a docgate validation run.
type Report struct {
	FilesChecked int
	Findings     []Finding
}

// OK reports whether validation completed with zero findings.
func (r *Report) OK() bool {
	return len(r.Findings) == 0
}

// Format renders a deterministic human-readable summary of all findings.
func (r *Report) Format() string {
	if r.OK() {
		return fmt.Sprintf("doc-gate: OK (%d markdown files checked, 0 findings)\n", r.FilesChecked)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "doc-gate: FAILED (%d finding(s) across %d markdown files):\n", len(r.Findings), r.FilesChecked)
	for _, f := range r.Findings {
		if f.Line > 0 {
			fmt.Fprintf(&b, "  - [%s] %s:%d: %s\n", f.Rule, f.Path, f.Line, f.Message)
			continue
		}
		fmt.Fprintf(&b, "  - [%s] %s: %s\n", f.Rule, f.Path, f.Message)
	}
	return b.String()
}

// Check runs all Markdown link, ADR, SPEC, verification ledger, and drift checks against rootDir.
func Check(rootDir string) (*Report, error) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolving root directory: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("stating root directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root path %q is not a directory", absRoot)
	}

	report := &Report{}
	filesChecked, linkFindings, err := checkMarkdownLinks(absRoot)
	if err != nil {
		return nil, err
	}
	report.FilesChecked = filesChecked
	report.Findings = append(report.Findings, linkFindings...)

	for _, fn := range []func(string) ([]Finding, error){
		checkADRs,
		checkSPECs,
		checkVerificationLedger,
		checkCodeDocDrift,
	} {
		findings, checkErr := fn(absRoot)
		if checkErr != nil {
			return nil, checkErr
		}
		report.Findings = append(report.Findings, findings...)
	}

	sort.Slice(report.Findings, func(i, j int) bool {
		a, b := report.Findings[i], report.Findings[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Message < b.Message
	})
	return report, nil
}

func resolveWithinRoot(absRoot, relPath string) (string, error) {
	cleanRel := filepath.Clean(filepath.FromSlash(relPath))
	if filepath.IsAbs(cleanRel) || cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes repository root", relPath)
	}
	joined := filepath.Join(absRoot, cleanRel)
	relCheck, err := filepath.Rel(absRoot, joined)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes repository root", relPath)
	}
	return joined, nil
}

func readRepoFile(absRoot, relPath string) ([]byte, error) {
	joined, err := resolveWithinRoot(absRoot, relPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(joined)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxDocFileBytes {
		return nil, fmt.Errorf("file %s exceeds %d byte limit", relPath, maxDocFileBytes)
	}
	// #nosec G304 -- joined is bounded inside absRoot via resolveWithinRoot.
	return os.ReadFile(joined)
}

func repoPathExists(absRoot, relPath string) bool {
	joined, err := resolveWithinRoot(absRoot, relPath)
	if err != nil {
		return false
	}
	_, err = os.Stat(joined)
	return err == nil
}
