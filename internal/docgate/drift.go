package docgate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	cliRefRelPath    = "docs/cli-reference.md"
	archDocRelPath   = "docs/architecture.md"
	goFormatRelPath  = "internal/format/format.go"
	rustFormatRel    = "experiments/v3lab/rust-stub/src/format.rs"
	rustProdFmtRel   = "rust/crates/microfat-stub-core/src/format.rs"
	cobraUseFieldPat = `Use:\s*"([^"]+)"`
)

var (
	cobraUseRe        = regexp.MustCompile(cobraUseFieldPat)
	goEnvConstRe      = regexp.MustCompile(`Env[A-Za-z0-9]+\s*=\s*"(MICROFAT_[A-Z0-9_]+)"`)
	goTrailerSizeRe   = regexp.MustCompile(`(?m)\bTrailerSize\s*=\s*(\d+)`)
	goFormatVer2Re    = regexp.MustCompile(`(?m)\bFormatVersion2\s*=\s*(\d+)`)
	goMagicStringRe   = regexp.MustCompile(`(?m)\bMagicString\s*=\s*"([^"]+)"`)
	goIndexMagicV2Re  = regexp.MustCompile(`(?m)\bIndexMagicV2\s*=\s*"([^"]+)"`)
	rsTrailerSizeRe   = regexp.MustCompile(`TRAILER_SIZE:\s*usize\s*=\s*(\d+)`)
	rsFormatVer2Re    = regexp.MustCompile(`FORMAT_VERSION_2:\s*u16\s*=\s*(\d+)`)
	rsTrailerMagicRe  = regexp.MustCompile(`TRAILER_MAGIC:[^=]+=\s*b"([^"]+)"`)
	rsIndexMagicV2Re  = regexp.MustCompile(`INDEX_MAGIC_V2:[^=]+=\s*b"([^"]+)"`)
	rsMaxIndexSizeRe  = regexp.MustCompile(`MAX_INDEX_SIZE:\s*u64\s*=\s*1024\s*\*\s*1024`)
	goMaxIndexSizeRe  = regexp.MustCompile(`MaxIndexSize\s*=\s*1024\s*\*\s*1024`)
	hiddenCobraLineRe = regexp.MustCompile(`Hidden:\s*true`)
)

func checkCodeDocDrift(absRoot string) ([]Finding, error) {
	var findings []Finding

	cliData, err := readRepoFile(absRoot, cliRefRelPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", cliRefRelPath, err)
	}
	cliDoc := string(cliData)

	goFormatData, err := readRepoFile(absRoot, goFormatRelPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", goFormatRelPath, err)
	}
	goFormat := string(goFormatData)

	cmdFindings, err := checkCLICommandDrift(absRoot, cliDoc)
	if err != nil {
		return nil, err
	}
	findings = append(findings, cmdFindings...)
	findings = append(findings, checkEnvVarDrift(goFormat, cliDoc)...)

	archData, err := readRepoFile(absRoot, archDocRelPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", archDocRelPath, err)
	}
	findings = append(findings, checkArchitectureConstantDrift(goFormat, string(archData))...)

	for _, rustPath := range []string{rustProdFmtRel, rustFormatRel} {
		if repoPathExists(absRoot, rustPath) {
			rsData, rsErr := readRepoFile(absRoot, rustPath)
			if rsErr != nil {
				return nil, rsErr
			}
			findings = append(findings, checkRustFormatDrift(rustPath, string(rsData), goFormat)...)
		}
	}

	return findings, nil
}

func checkCLICommandDrift(absRoot, cliDoc string) ([]Finding, error) {
	cmdDir := filepath.Join(absRoot, "cmd", "microfat")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return nil, fmt.Errorf("reading cmd/microfat: %w", err)
	}

	topLevel := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "benchmark_experiment.go" {
			continue
		}
		data, readErr := readRepoFile(absRoot, "cmd/microfat/"+name)
		if readErr != nil {
			return nil, readErr
		}
		for _, line := range strings.Split(string(data), "\n") {
			if hiddenCobraLineRe.MatchString(line) {
				continue
			}
			if m := cobraUseRe.FindStringSubmatch(line); len(m) == submatchOneGroup {
				cmdName := strings.Fields(m[1])[0]
				if cmdName != "microfat" && cmdName != "" {
					topLevel[cmdName] = struct{}{}
				}
			}
		}
	}

	var names []string
	for name := range topLevel {
		names = append(names, name)
	}
	sort.Strings(names)

	var findings []Finding
	for _, cmdName := range names {
		expectedHeading := "## `" + cmdName + "`"
		expectedFull := "`microfat " + cmdName
		if !strings.Contains(cliDoc, expectedFull) && !strings.Contains(cliDoc, expectedHeading) {
			findings = append(findings, Finding{
				Path:    cliRefRelPath,
				Rule:    "drift-cli-command",
				Message: fmt.Sprintf("CLI command %q from cmd/microfat is not documented in %s", cmdName, cliRefRelPath),
			})
		}
	}
	return findings, nil
}

func checkEnvVarDrift(goFormat, cliDoc string) []Finding {
	var findings []Finding
	seen := make(map[string]struct{})
	for _, m := range goEnvConstRe.FindAllStringSubmatch(goFormat, -1) {
		envName := m[1]
		if _, dup := seen[envName]; dup {
			continue
		}
		seen[envName] = struct{}{}
		if !strings.Contains(cliDoc, envName) {
			findings = append(findings, Finding{
				Path:    cliRefRelPath,
				Rule:    "drift-env-var",
				Message: fmt.Sprintf("environment variable %s defined in %s is undocumented in %s", envName, goFormatRelPath, cliRefRelPath),
			})
		}
	}
	return findings
}

func checkArchitectureConstantDrift(goFormat, archDoc string) []Finding {
	var findings []Finding
	trailerMatch := goTrailerSizeRe.FindStringSubmatch(goFormat)
	magicMatch := goMagicStringRe.FindStringSubmatch(goFormat)
	idxMagicMatch := goIndexMagicV2Re.FindStringSubmatch(goFormat)
	verMatch := goFormatVer2Re.FindStringSubmatch(goFormat)

	if len(trailerMatch) != 2 || len(magicMatch) != 2 || len(idxMagicMatch) != 2 || len(verMatch) != 2 {
		return []Finding{{
			Path:    goFormatRelPath,
			Rule:    "drift-format-parse",
			Message: "failed to parse canonical Format v2 constants from Go source",
		}}
	}

	if !strings.Contains(archDoc, trailerMatch[1]+"-Byte") && !strings.Contains(archDoc, trailerMatch[1]+"-byte") {
		findings = append(findings, Finding{
			Path:    archDocRelPath,
			Rule:    "drift-format-doc",
			Message: fmt.Sprintf("TrailerSize (%s bytes) not reflected in %s", trailerMatch[1], archDocRelPath),
		})
	}
	if !strings.Contains(archDoc, magicMatch[1]) {
		findings = append(findings, Finding{
			Path:    archDocRelPath,
			Rule:    "drift-format-doc",
			Message: fmt.Sprintf("MagicString (%s) missing from %s", magicMatch[1], archDocRelPath),
		})
	}
	if !strings.Contains(archDoc, idxMagicMatch[1]) {
		findings = append(findings, Finding{
			Path:    archDocRelPath,
			Rule:    "drift-format-doc",
			Message: fmt.Sprintf("IndexMagicV2 (%s) missing from %s", idxMagicMatch[1], archDocRelPath),
		})
	}
	return findings
}

func checkRustFormatDrift(rustPath, rsFormat, goFormat string) []Finding {
	var findings []Finding
	checks := []struct {
		name  string
		goVal string
		rsVal string
	}{
		{"TRAILER_SIZE", firstSubmatch(goTrailerSizeRe, goFormat), firstSubmatch(rsTrailerSizeRe, rsFormat)},
		{"FORMAT_VERSION_2", firstSubmatch(goFormatVer2Re, goFormat), firstSubmatch(rsFormatVer2Re, rsFormat)},
		{"TRAILER_MAGIC", firstSubmatch(goMagicStringRe, goFormat), firstSubmatch(rsTrailerMagicRe, rsFormat)},
		{"INDEX_MAGIC_V2", firstSubmatch(goIndexMagicV2Re, goFormat), firstSubmatch(rsIndexMagicV2Re, rsFormat)},
	}
	for _, c := range checks {
		if c.goVal == "" || c.rsVal == "" || c.goVal != c.rsVal {
			findings = append(findings, Finding{
				Path:    rustPath,
				Rule:    "drift-rust-format",
				Message: fmt.Sprintf("Rust constant %s (%q) diverges from Go %s (%q)", c.name, c.rsVal, goFormatRelPath, c.goVal),
			})
		}
	}
	if goMaxIndexSizeRe.MatchString(goFormat) != rsMaxIndexSizeRe.MatchString(rsFormat) {
		findings = append(findings, Finding{
			Path:    rustPath,
			Rule:    "drift-rust-format",
			Message: "Rust MAX_INDEX_SIZE diverges from Go MaxIndexSize",
		})
	}
	return findings
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < submatchOneGroup {
		return ""
	}
	return m[1]
}
