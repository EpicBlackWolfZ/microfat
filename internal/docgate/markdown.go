package docgate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var (
	inlineLinkRe  = regexp.MustCompile(`!?\[[^\]]*\]\(([^)]+)\)`)
	refDefRe      = regexp.MustCompile(`^\s*\[([^\]]+)\]:\s*(\S+)`)
	atxHeadingRe  = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.+?)\s*$`)
	inlineCodeRe  = regexp.MustCompile("`[^`]*`")
	linkTextRe    = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	trailingHash  = regexp.MustCompile(`\s+#+\s*$`)
	htmlTagRe     = regexp.MustCompile(`<[^>]+>`)
	fenceMarkerRe = regexp.MustCompile("^\\s{0,3}(`{3,}|~{3,})")
)

type parsedMarkdown struct {
	relPath string
	slugs   map[string]struct{}
	links   []mdLink
}

type mdLink struct {
	line   int
	target string
}

func discoverMarkdownFiles(absRoot string) ([]string, error) {
	var files []string
	entries, err := os.ReadDir(absRoot)
	if err != nil {
		return nil, fmt.Errorf("reading root directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			files = append(files, entry.Name())
		}
	}

	docsDir := filepath.Join(absRoot, "docs")
	if info, statErr := os.Stat(docsDir); statErr == nil && info.IsDir() {
		walkErr := filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, wErr error) error {
			if wErr != nil {
				return wErr
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(d.Name(), ".md") {
				rel, relErr := filepath.Rel(absRoot, path)
				if relErr != nil {
					return relErr
				}
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("walking docs directory: %w", walkErr)
		}
	}

	sort.Strings(files)
	return files, nil
}

func checkMarkdownLinks(absRoot string) (int, []Finding, error) {
	files, err := discoverMarkdownFiles(absRoot)
	if err != nil {
		return 0, nil, err
	}

	parsed := make(map[string]*parsedMarkdown, len(files))
	for _, rel := range files {
		data, readErr := readRepoFile(absRoot, rel)
		if readErr != nil {
			return 0, nil, fmt.Errorf("reading %s: %w", rel, readErr)
		}
		parsed[rel] = parseMarkdownDocument(rel, string(data))
	}

	var findings []Finding
	for _, rel := range files {
		doc := parsed[rel]
		for _, link := range doc.links {
			if f, bad := validateLink(absRoot, parsed, doc, link); bad {
				findings = append(findings, f)
			}
		}
	}
	return len(files), findings, nil
}

func parseMarkdownDocument(relPath, content string) *parsedMarkdown {
	doc := &parsedMarkdown{
		relPath: relPath,
		slugs:   make(map[string]struct{}),
	}
	slugCounts := make(map[string]int)
	var fenceChar byte
	var fenceLen int

	lines := strings.Split(content, "\n")
	for idx, rawLine := range lines {
		lineNum := idx + 1
		if m := fenceMarkerRe.FindStringSubmatch(rawLine); len(m) == submatchOneGroup {
			marker := m[1]
			if fenceLen == 0 {
				fenceChar = marker[0]
				fenceLen = len(marker)
				continue
			}
			if marker[0] == fenceChar && len(marker) >= fenceLen && strings.TrimSpace(strings.TrimLeft(rawLine, " \t"+string(fenceChar))) == "" {
				fenceLen = 0
				fenceChar = 0
				continue
			}
		}
		if fenceLen > 0 {
			continue
		}

		if hm := atxHeadingRe.FindStringSubmatch(rawLine); len(hm) == submatchTwoGroups {
			base := gfmSlug(hm[2])
			if base != "" {
				count := slugCounts[base]
				slugCounts[base] = count + 1
				slug := base
				if count > 0 {
					slug = fmt.Sprintf("%s-%d", base, count)
				}
				doc.slugs[slug] = struct{}{}
			}
		}

		if rm := refDefRe.FindStringSubmatch(rawLine); len(rm) == submatchTwoGroups {
			doc.links = append(doc.links, mdLink{line: lineNum, target: cleanLinkTarget(rm[2])})
		}

		stripped := inlineCodeRe.ReplaceAllString(rawLine, "")
		for _, lm := range inlineLinkRe.FindAllStringSubmatch(stripped, -1) {
			if len(lm) == submatchOneGroup {
				doc.links = append(doc.links, mdLink{line: lineNum, target: cleanLinkTarget(lm[1])})
			}
		}
	}
	return doc
}

func cleanLinkTarget(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "<") {
		if end := strings.IndexByte(trimmed, '>'); end > 1 {
			return strings.TrimSpace(trimmed[1:end])
		}
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func shouldSkipTarget(target string) bool {
	if target == "" || target == "#" {
		return true
	}
	lower := strings.ToLower(target)
	for _, prefix := range []string{"http://", "https://", "mailto:", "file://", "data:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return strings.ContainsAny(target, "*{}")
}

func validateLink(absRoot string, parsed map[string]*parsedMarkdown, doc *parsedMarkdown, link mdLink) (Finding, bool) {
	if shouldSkipTarget(link.target) {
		return Finding{}, false
	}

	pathPart, anchor, hasAnchor := strings.Cut(link.target, "#")
	if pathPart == "" {
		if _, ok := doc.slugs[anchor]; !ok {
			return Finding{
				Path:    doc.relPath,
				Line:    link.line,
				Rule:    "broken-anchor",
				Message: fmt.Sprintf("anchor #%s does not match any heading in %s", anchor, doc.relPath),
			}, true
		}
		return Finding{}, false
	}

	baseDir := filepath.Dir(filepath.FromSlash(doc.relPath))
	joined := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(pathPart)))
	relTarget := filepath.ToSlash(joined)
	if relTarget == ".." || strings.HasPrefix(relTarget, "../") || filepath.IsAbs(pathPart) {
		return Finding{
			Path:    doc.relPath,
			Line:    link.line,
			Rule:    "path-escape",
			Message: fmt.Sprintf("link target %q escapes repository root", link.target),
		}, true
	}

	if !repoPathExists(absRoot, relTarget) {
		return Finding{
			Path:    doc.relPath,
			Line:    link.line,
			Rule:    "broken-link",
			Message: fmt.Sprintf("target %q (resolved to %s) does not exist", link.target, relTarget),
		}, true
	}

	if hasAnchor && anchor != "" && strings.HasSuffix(strings.ToLower(relTarget), ".md") {
		targetDoc, ok := parsed[relTarget]
		if !ok {
			data, err := readRepoFile(absRoot, relTarget)
			if err != nil {
				return Finding{
					Path:    doc.relPath,
					Line:    link.line,
					Rule:    "broken-link",
					Message: fmt.Sprintf("unable to read anchor target %s: %v", relTarget, err),
				}, true
			}
			targetDoc = parseMarkdownDocument(relTarget, string(data))
			parsed[relTarget] = targetDoc
		}
		if _, exists := targetDoc.slugs[anchor]; !exists {
			return Finding{
				Path:    doc.relPath,
				Line:    link.line,
				Rule:    "broken-anchor",
				Message: fmt.Sprintf("anchor #%s not found in %s", anchor, relTarget),
			}, true
		}
	}

	return Finding{}, false
}

func gfmSlug(heading string) string {
	s := trailingHash.ReplaceAllString(strings.TrimSpace(heading), "")
	s = linkTextRe.ReplaceAllString(s, "$1")
	s = stripHTMLOutsideCode(s)
	s = strings.ToLower(strings.TrimSpace(s))

	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func stripHTMLOutsideCode(s string) string {
	var b strings.Builder
	rest := s
	for len(rest) > 0 {
		start := strings.IndexByte(rest, '`')
		if start < 0 {
			b.WriteString(htmlTagRe.ReplaceAllString(rest, ""))
			break
		}
		b.WriteString(htmlTagRe.ReplaceAllString(rest[:start], ""))
		afterStart := rest[start+1:]
		end := strings.IndexByte(afterStart, '`')
		if end < 0 {
			b.WriteString(afterStart)
			break
		}
		b.WriteString(afterStart[:end])
		rest = afterStart[end+1:]
	}
	return b.String()
}
