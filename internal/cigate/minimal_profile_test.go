package cigate

import (
	"bufio"
	"go/build/constraint"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const minimalTagName = "minimal"

func exprMentionsTag(expr constraint.Expr, tag string) bool {
	switch node := expr.(type) {
	case *constraint.TagExpr:
		return node.Tag == tag
	case *constraint.NotExpr:
		return exprMentionsTag(node.X, tag)
	case *constraint.AndExpr:
		return exprMentionsTag(node.X, tag) || exprMentionsTag(node.Y, tag)
	case *constraint.OrExpr:
		return exprMentionsTag(node.X, tag) || exprMentionsTag(node.Y, tag)
	default:
		return false
	}
}

func fileMentionsBuildTag(path, tag string) (bool, error) {
	file, err := os.Open(path) // #nosec G304 -- repository source scan in unit tests.
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "package ") {
			break
		}
		if constraint.IsGoBuild(line) || constraint.IsPlusBuild(line) {
			expr, parseErr := constraint.Parse(line)
			if parseErr != nil {
				return false, parseErr
			}
			if exprMentionsTag(expr, tag) {
				return true, nil
			}
		}
	}
	return false, scanner.Err()
}

func TestBuildTagScannerDetectsMinimalExpressions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "minimal", content: "//go:build minimal\n\npackage p\n", want: true},
		{name: "negated_minimal", content: "//go:build linux && !minimal\n\npackage p\n", want: true},
		{name: "nested_or", content: "//go:build (linux && minimal) || darwin\n\npackage p\n", want: true},
		{name: "legacy_plus_build", content: "// +build !minimal\n\npackage p\n", want: true},
		{name: "unrelated_linux", content: "//go:build linux\n\npackage p\n", want: false},
		{name: "body_comment_ignored", content: "package p\n//go:build minimal\n", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(dir, tc.name+".go")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))
			got, err := fileMentionsBuildTag(path, minimalTagName)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestOnlyStubDeclaresMinimalBuildConstraint(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Join("..", "..")
	skipDirs := map[string]bool{".git": true, ".work": true, "bin": true, "dist": true, "vendor": true}

	var stubFiles, unexpectedFiles []string
	err := filepath.WalkDir(repoRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if skipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		mentions, err := fileMentionsBuildTag(path, minimalTagName)
		if err != nil {
			return err
		}
		if !mentions {
			return nil
		}
		if strings.HasPrefix(rel, "cmd/microfat-stub/") {
			stubFiles = append(stubFiles, rel)
		} else {
			unexpectedFiles = append(unexpectedFiles, rel)
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, stubFiles, "expected cmd/microfat-stub to declare minimal build constraints")
	require.Empty(t, unexpectedFiles,
		"minimal build constraints outside cmd/microfat-stub require updating scripts/test-profiles.sh minimal_packages")
}

func TestGoroutineLeakChecksRunInUnitAndIntegrationStages(t *testing.T) {
	t.Parallel()
	w := readWorkflow(t, "ci.yml")

	checkStepEnv := func(jobName, commandSubstr string) {
		t.Helper()
		job, ok := w.Jobs[jobName]
		require.True(t, ok, "missing job %s", jobName)
		matched := false
		for _, step := range job.Steps {
			if strings.Contains(step.Run, commandSubstr) {
				matched = true
				assert.Equal(t, "1", step.Env["MICROFAT_TEST_LEAKS"],
					"job %s step running %q must enable MICROFAT_TEST_LEAKS=1", jobName, commandSubstr)
				assert.Nil(t, step.ContinueOnError)
			}
		}
		assert.True(t, matched, "job %s missing step running %q", jobName, commandSubstr)
	}

	checkStepEnv("test", "task coverage-unit")
	checkStepEnv("integration", "task e2e")

	dxJob, ok := w.Jobs["dx"]
	require.True(t, ok)
	var hasDX, hasCheckLeaks, hasDemoCheck bool
	for _, step := range dxJob.Steps {
		assert.NotContains(t, step.Run, "task test-leaks",
			"dx must not duplicate full-suite leak tests already enforced in test and integration")
		if strings.Contains(step.Run, "task test-dx") {
			hasDX = true
		}
		if strings.Contains(step.Run, "task check-leaks") {
			hasCheckLeaks = true
		}
		if strings.Contains(step.Run, "task demo-check") {
			hasDemoCheck = true
		}
	}
	assert.True(t, hasDX, "dx must retain task test-dx")
	assert.True(t, hasCheckLeaks, "dx must retain task check-leaks")
	assert.True(t, hasDemoCheck, "dx must retain task demo-check")
}
