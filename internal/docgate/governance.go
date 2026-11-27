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
	adrIndexRelPath     = "docs/adr/README.md"
	adrTemplateRelPath  = "docs/adr/TEMPLATE.md"
	adrLegacy0001Path   = "docs/design/adr-0001-formal-verification-and-v3-lab.md"
	specIndexRelPath    = "docs/specs/README.md"
	specTemplateRelPath = "docs/specs/TEMPLATE.md"
	ledgerRelPath       = "docs/verification/README.md"
	toolPinsRelPath     = ".config/tool-versions.env"
	taskfileRelPath     = "Taskfile.yml"
	ciWorkflowRelPath   = ".github/workflows/ci.yml"
	relWorkflowRelPath  = ".github/workflows/release.yml"
)

var (
	adrFilenameRe   = regexp.MustCompile(`^ADR-(\d{4})-[a-z0-9]+(?:-[a-z0-9]+)*\.md$`)
	specFilenameRe  = regexp.MustCompile(`^SPEC-(\d{4})-[a-z0-9]+(?:-[a-z0-9]+)*\.md$`)
	isoDateRe       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
	reqDefRe        = regexp.MustCompile("`((?:REQ)-[A-Z0-9]+-[A-Z0-9-]+)`\\s*:")
	specLawDefRe    = regexp.MustCompile("`((?:LAW)-[A-Z0-9]+-[A-Z0-9-]+)`\\s*:")
	ledgerLawRe     = regexp.MustCompile("`(LAW-[A-Z0-9]+-[A-Z0-9-]+)`")
	backtickTokenRe = regexp.MustCompile("`([^`]+)`")
	toolPinRefRe    = regexp.MustCompile(`\b([A-Z][A-Z0-9_]*_(?:AMD64|ARM64|VERSION))\b`)
	ciGateStepRe    = regexp.MustCompile(`\b(ci\.yml|release\.yml):([a-z0-9-]+)\b`)
	taskTargetRe    = regexp.MustCompile(`(?m)^  ([a-zA-Z0-9_-]+):`)
	workflowIDRe    = regexp.MustCompile(`(?m)^\s*(?:-\s*)?id:\s*([a-zA-Z0-9_-]+)\s*$`)
)

func checkADRs(absRoot string) ([]Finding, error) {
	var findings []Finding
	for _, req := range []string{adrIndexRelPath, adrTemplateRelPath, adrLegacy0001Path} {
		if !repoPathExists(absRoot, req) {
			findings = append(findings, Finding{
				Path:    req,
				Rule:    "adr-structure",
				Message: "required ADR governance file is missing",
			})
		}
	}
	if len(findings) > 0 {
		return findings, nil
	}

	adrFiles, dirFindings, err := collectADRFiles(absRoot)
	if err != nil {
		return nil, err
	}
	findings = append(findings, dirFindings...)

	seenNumbers := map[string]string{"0001": adrLegacy0001Path}
	legacyData, err := readRepoFile(absRoot, adrLegacy0001Path)
	if err != nil {
		return nil, err
	}
	findings = append(findings, validateADRDoc(adrLegacy0001Path, string(legacyData), true)...)

	for _, entry := range adrFiles {
		if prev, dup := seenNumbers[entry.num]; dup {
			findings = append(findings, Finding{
				Path:    entry.relPath,
				Rule:    "adr-duplicate-number",
				Message: fmt.Sprintf("ADR number %s already used by %s", entry.num, prev),
			})
		} else {
			seenNumbers[entry.num] = entry.relPath
		}
		data, readErr := readRepoFile(absRoot, entry.relPath)
		if readErr != nil {
			return nil, readErr
		}
		findings = append(findings, validateADRDoc(entry.relPath, string(data), false)...)
	}

	indexData, err := readRepoFile(absRoot, adrIndexRelPath)
	if err != nil {
		return nil, err
	}
	findings = append(findings, checkADRIndexSync(adrFiles, string(indexData))...)
	return findings, nil
}

type numberedDoc struct {
	num     string
	relPath string
}

func collectADRFiles(absRoot string) ([]numberedDoc, []Finding, error) {
	entries, err := os.ReadDir(filepath.Join(absRoot, "docs", "adr"))
	if err != nil {
		return nil, nil, err
	}
	var docs []numberedDoc
	var findings []Finding
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "README.md" || name == "TEMPLATE.md" || !strings.HasSuffix(name, ".md") {
			continue
		}
		rel := "docs/adr/" + name
		m := adrFilenameRe.FindStringSubmatch(name)
		if len(m) != submatchOneGroup {
			findings = append(findings, Finding{
				Path:    rel,
				Rule:    "adr-filename",
				Message: "ADR filename must match ADR-NNNN-kebab-slug.md",
			})
			continue
		}
		docs = append(docs, numberedDoc{num: m[1], relPath: rel})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].relPath < docs[j].relPath })
	return docs, findings, nil
}

func validateADRDoc(relPath, content string, legacy bool) []Finding {
	var findings []Finding
	meta := extractMetadata(content)
	requiredMeta := []string{"Status", "Date", "Related Issues"}
	if !legacy {
		requiredMeta = append(requiredMeta, "Deciders")
	}
	for _, key := range requiredMeta {
		val := strings.TrimSpace(meta[key])
		if val == "" {
			findings = append(findings, Finding{
				Path:    relPath,
				Rule:    "adr-metadata",
				Message: fmt.Sprintf("missing required metadata field '- **%s**:'", key),
			})
			continue
		}
		if key == "Date" && !isoDateRe.MatchString(val) {
			findings = append(findings, Finding{
				Path:    relPath,
				Rule:    "adr-metadata",
				Message: fmt.Sprintf("ADR Date %q must begin with YYYY-MM-DD", val),
			})
		}
	}

	headings := extractHeadingSet(content)
	for _, h := range []string{"context", "decision"} {
		if !hasHeadingSubstring(headings, h) {
			findings = append(findings, Finding{
				Path:    relPath,
				Rule:    "adr-section",
				Message: fmt.Sprintf("missing required section containing %q", h),
			})
		}
	}
	if !hasHeadingSubstring(headings, "rollback") {
		findings = append(findings, Finding{
			Path:    relPath,
			Rule:    "adr-section",
			Message: "missing required rollback section",
		})
	}
	return findings
}

func checkADRIndexSync(adrFiles []numberedDoc, indexContent string) []Finding {
	var findings []Finding
	doc := parseMarkdownDocument(adrIndexRelPath, indexContent)
	linked := make(map[string]struct{}, len(doc.links))
	for _, l := range doc.links {
		pathPart, _, _ := strings.Cut(l.target, "#")
		if pathPart == "" || shouldSkipTarget(l.target) {
			continue
		}
		resolved := filepath.ToSlash(filepath.Clean(filepath.Join("docs/adr", filepath.FromSlash(pathPart))))
		linked[resolved] = struct{}{}
	}
	if _, ok := linked[adrLegacy0001Path]; !ok {
		findings = append(findings, Finding{
			Path:    adrIndexRelPath,
			Rule:    "adr-index-sync",
			Message: fmt.Sprintf("ADR index must link %s", adrLegacy0001Path),
		})
	}
	for _, d := range adrFiles {
		if _, ok := linked[d.relPath]; !ok {
			findings = append(findings, Finding{
				Path:    adrIndexRelPath,
				Rule:    "adr-index-sync",
				Message: fmt.Sprintf("ADR index does not link %s", d.relPath),
			})
		}
	}
	return findings
}

func checkSPECs(absRoot string) ([]Finding, error) {
	var findings []Finding
	for _, req := range []string{specIndexRelPath, specTemplateRelPath} {
		if !repoPathExists(absRoot, req) {
			findings = append(findings, Finding{
				Path:    req,
				Rule:    "spec-structure",
				Message: "required SPEC governance file is missing",
			})
		}
	}
	if len(findings) > 0 {
		return findings, nil
	}

	entries, err := os.ReadDir(filepath.Join(absRoot, "docs", "specs"))
	if err != nil {
		return nil, err
	}
	var specFiles []numberedDoc
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "README.md" || name == "TEMPLATE.md" || !strings.HasSuffix(name, ".md") {
			continue
		}
		rel := "docs/specs/" + name
		m := specFilenameRe.FindStringSubmatch(name)
		if len(m) != submatchOneGroup {
			findings = append(findings, Finding{
				Path:    rel,
				Rule:    "spec-filename",
				Message: "SPEC filename must match SPEC-NNNN-kebab-slug.md",
			})
			continue
		}
		specFiles = append(specFiles, numberedDoc{num: m[1], relPath: rel})
	}
	sort.Slice(specFiles, func(i, j int) bool { return specFiles[i].relPath < specFiles[j].relPath })

	seenNumbers := make(map[string]string, len(specFiles))
	seenIDs := make(map[string]string)
	for _, entry := range specFiles {
		if prev, dup := seenNumbers[entry.num]; dup {
			findings = append(findings, Finding{
				Path:    entry.relPath,
				Rule:    "spec-duplicate-number",
				Message: fmt.Sprintf("SPEC number %s already used by %s", entry.num, prev),
			})
		} else {
			seenNumbers[entry.num] = entry.relPath
		}
		data, readErr := readRepoFile(absRoot, entry.relPath)
		if readErr != nil {
			return nil, readErr
		}
		findings = append(findings, validateSPECDoc(entry.relPath, string(data), seenIDs)...)
	}

	indexData, err := readRepoFile(absRoot, specIndexRelPath)
	if err != nil {
		return nil, err
	}
	doc := parseMarkdownDocument(specIndexRelPath, string(indexData))
	linked := make(map[string]struct{}, len(doc.links))
	for _, l := range doc.links {
		pathPart, _, _ := strings.Cut(l.target, "#")
		if pathPart == "" || shouldSkipTarget(l.target) {
			continue
		}
		resolved := filepath.ToSlash(filepath.Clean(filepath.Join("docs/specs", filepath.FromSlash(pathPart))))
		linked[resolved] = struct{}{}
	}
	for _, d := range specFiles {
		if _, ok := linked[d.relPath]; !ok {
			findings = append(findings, Finding{
				Path:    specIndexRelPath,
				Rule:    "spec-index-sync",
				Message: fmt.Sprintf("SPEC index does not link %s", d.relPath),
			})
		}
	}
	return findings, nil
}

func validateSPECDoc(relPath, content string, seenIDs map[string]string) []Finding {
	var findings []Finding
	meta := extractMetadata(content)
	for _, key := range []string{"Spec ID", "Status", "Date", "Related ADRs", "Verified By"} {
		val := strings.TrimSpace(meta[key])
		if val == "" {
			findings = append(findings, Finding{
				Path:    relPath,
				Rule:    "spec-metadata",
				Message: fmt.Sprintf("missing required metadata field '- **%s**:'", key),
			})
		} else if key == "Date" && !isoDateRe.MatchString(val) {
			findings = append(findings, Finding{
				Path:    relPath,
				Rule:    "spec-metadata",
				Message: fmt.Sprintf("SPEC Date %q must begin with YYYY-MM-DD", val),
			})
		}
	}

	headings := extractHeadingSet(content)
	for _, h := range []string{"scope", "normative requirements", "traceability"} {
		if !hasHeadingSubstring(headings, h) {
			findings = append(findings, Finding{
				Path:    relPath,
				Rule:    "spec-section",
				Message: fmt.Sprintf("missing required section containing %q", h),
			})
		}
	}

	reqCount := 0
	for idx, line := range strings.Split(content, "\n") {
		lineNum := idx + 1
		for _, re := range []*regexp.Regexp{reqDefRe, specLawDefRe} {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				id := m[1]
				if strings.HasPrefix(id, "REQ-") {
					reqCount++
				}
				if prev, dup := seenIDs[id]; dup {
					findings = append(findings, Finding{
						Path:    relPath,
						Line:    lineNum,
						Rule:    "spec-duplicate-id",
						Message: fmt.Sprintf("identifier %s already defined in %s", id, prev),
					})
				} else {
					seenIDs[id] = relPath
				}
			}
		}
	}
	if reqCount == 0 {
		findings = append(findings, Finding{
			Path:    relPath,
			Rule:    "spec-requirements",
			Message: "SPEC must define at least one REQ-* identifier",
		})
	}
	return findings
}

func checkVerificationLedger(absRoot string) ([]Finding, error) {
	if !repoPathExists(absRoot, ledgerRelPath) {
		return []Finding{{
			Path:    ledgerRelPath,
			Rule:    "ledger-missing",
			Message: "required formal verification ledger is missing",
		}}, nil
	}

	ledgerData, err := readRepoFile(absRoot, ledgerRelPath)
	if err != nil {
		return nil, err
	}
	toolPins, err := loadToolPinKeys(absRoot)
	if err != nil {
		return nil, err
	}
	taskTargets, err := loadTaskTargets(absRoot)
	if err != nil {
		return nil, err
	}
	ciSteps, err := loadWorkflowStepIDs(absRoot, ciWorkflowRelPath)
	if err != nil {
		return nil, err
	}
	releaseSteps, err := loadWorkflowStepIDs(absRoot, relWorkflowRelPath)
	if err != nil {
		return nil, err
	}

	var findings []Finding
	seenLaws := make(map[string]int)
	for idx, line := range strings.Split(string(ledgerData), "\n") {
		lineNum := idx + 1
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		lm := ledgerLawRe.FindStringSubmatch(line)
		if len(lm) != submatchOneGroup {
			continue
		}
		lawID := lm[1]
		if prevLine, dup := seenLaws[lawID]; dup {
			findings = append(findings, Finding{
				Path:    ledgerRelPath,
				Line:    lineNum,
				Rule:    "ledger-duplicate-law",
				Message: fmt.Sprintf("duplicate law ID %s (first defined on line %d)", lawID, prevLine),
			})
		} else {
			seenLaws[lawID] = lineNum
		}

		for _, pm := range toolPinRefRe.FindAllStringSubmatch(line, -1) {
			if _, ok := toolPins[pm[1]]; !ok {
				findings = append(findings, Finding{
					Path:    ledgerRelPath,
					Line:    lineNum,
					Rule:    "ledger-unknown-pin",
					Message: fmt.Sprintf("tool pin %s not found in %s", pm[1], toolPinsRelPath),
				})
			}
		}

		for _, tm := range backtickTokenRe.FindAllStringSubmatch(line, -1) {
			token := strings.TrimSpace(tm[1])
			if taskName, ok := strings.CutPrefix(token, "task "); ok {
				taskName = strings.TrimSpace(taskName)
				if _, exists := taskTargets[taskName]; !exists {
					findings = append(findings, Finding{
						Path:    ledgerRelPath,
						Line:    lineNum,
						Rule:    "ledger-unknown-task",
						Message: fmt.Sprintf("task target %q not found in %s", taskName, taskfileRelPath),
					})
				}
			}
			for _, gm := range ciGateStepRe.FindAllStringSubmatch(token, -1) {
				wf, stepID := gm[1], gm[2]
				steps := ciSteps
				if wf == "release.yml" {
					steps = releaseSteps
				}
				if _, exists := steps[stepID]; !exists {
					findings = append(findings, Finding{
						Path:    ledgerRelPath,
						Line:    lineNum,
						Rule:    "ledger-unknown-ci-step",
						Message: fmt.Sprintf("workflow step %s:%s does not exist", wf, stepID),
					})
				}
			}
		}
	}

	if len(seenLaws) == 0 {
		findings = append(findings, Finding{
			Path:    ledgerRelPath,
			Rule:    "ledger-empty",
			Message: "formal verification ledger must record at least one LAW-* entry",
		})
	}
	return findings, nil
}

func extractMetadata(content string) map[string]string {
	meta := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			break
		}
		if !strings.HasPrefix(trimmed, "- **") {
			continue
		}
		rest := strings.TrimPrefix(trimmed, "- **")
		key, val, ok := strings.Cut(rest, "**:")
		if !ok {
			continue
		}
		meta[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return meta
}

func extractHeadingSet(content string) []string {
	var headings []string
	for _, line := range strings.Split(content, "\n") {
		if m := atxHeadingRe.FindStringSubmatch(line); len(m) == submatchTwoGroups {
			headings = append(headings, strings.ToLower(strings.TrimSpace(m[2])))
		}
	}
	return headings
}

func hasHeadingSubstring(headings []string, needle string) bool {
	for _, h := range headings {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

func loadToolPinKeys(absRoot string) (map[string]struct{}, error) {
	data, err := readRepoFile(absRoot, toolPinsRelPath)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]struct{})
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if k, _, ok := strings.Cut(trimmed, "="); ok {
			keys[strings.TrimSpace(k)] = struct{}{}
		}
	}
	return keys, nil
}

func loadTaskTargets(absRoot string) (map[string]struct{}, error) {
	data, err := readRepoFile(absRoot, taskfileRelPath)
	if err != nil {
		return nil, err
	}
	targets := make(map[string]struct{})
	for _, m := range taskTargetRe.FindAllStringSubmatch(string(data), -1) {
		targets[m[1]] = struct{}{}
	}
	return targets, nil
}

func loadWorkflowStepIDs(absRoot, relPath string) (map[string]struct{}, error) {
	data, err := readRepoFile(absRoot, relPath)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{})
	for _, m := range workflowIDRe.FindAllStringSubmatch(string(data), -1) {
		ids[m[1]] = struct{}{}
	}
	return ids, nil
}
