package cigate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type codecovConfig struct {
	Codecov struct {
		RequireCIToPass bool `yaml:"require_ci_to_pass"`
	} `yaml:"codecov"`
	Coverage struct {
		Precision int    `yaml:"precision"`
		Round     string `yaml:"round"`
		Range     string `yaml:"range"`
		Status    struct {
			Project map[string]struct {
				Target        string   `yaml:"target"`
				Threshold     string   `yaml:"threshold"`
				Base          string   `yaml:"base"`
				Flags         []string `yaml:"flags"`
				Informational bool     `yaml:"informational"`
			} `yaml:"project"`
			Patch map[string]struct {
				Target        string   `yaml:"target"`
				Threshold     string   `yaml:"threshold"`
				Base          string   `yaml:"base"`
				Flags         []string `yaml:"flags"`
				Informational bool     `yaml:"informational"`
			} `yaml:"patch"`
		} `yaml:"status"`
	} `yaml:"coverage"`
	ComponentManagement struct {
		DefaultRules struct {
			Statuses []struct {
				Type      string `yaml:"type"`
				Target    string `yaml:"target"`
				Threshold string `yaml:"threshold"`
				Base      string `yaml:"base"`
			} `yaml:"statuses"`
		} `yaml:"default_rules"`
		IndividualComponents []struct {
			ComponentID string   `yaml:"component_id"`
			Name        string   `yaml:"name"`
			Paths       []string `yaml:"paths"`
		} `yaml:"individual_components"`
	} `yaml:"component_management"`
	FlagManagement struct {
		DefaultRules struct {
			Carryforward bool `yaml:"carryforward"`
			Statuses     []struct {
				Type      string `yaml:"type"`
				Target    string `yaml:"target"`
				Threshold string `yaml:"threshold"`
				Base      string `yaml:"base"`
			} `yaml:"statuses"`
		} `yaml:"default_rules"`
		IndividualFlags []struct {
			Name         string `yaml:"name"`
			Carryforward bool   `yaml:"carryforward"`
		} `yaml:"individual_flags"`
	} `yaml:"flag_management"`
	Comment struct {
		Layout                string `yaml:"layout"`
		Behavior              string `yaml:"behavior"`
		RequireChanges        bool   `yaml:"require_changes"`
		RequireHead           bool   `yaml:"require_head"`
		HideProjectCoverage   bool   `yaml:"hide_project_coverage"`
		ShowCarryforwardFlags bool   `yaml:"show_carryforward_flags"`
	} `yaml:"comment"`
	Ignore       []string                  `yaml:"ignore"`
	Parsers      map[string]map[string]any `yaml:"parsers"`
	GitHubChecks struct {
		Annotations bool `yaml:"annotations"`
	} `yaml:"github_checks"`
}

func readCodecovConfig(t *testing.T) codecovConfig {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "codecov.yml"))
	require.NoError(t, err, "codecov.yml must exist at repository root")
	var cfg codecovConfig
	require.NoError(t, yaml.Unmarshal(data, &cfg), "codecov.yml must be valid YAML")
	return cfg
}

func validateCodecovConfig(cfg codecovConfig) error {
	if !cfg.Codecov.RequireCIToPass {
		return errors.New("codecov.require_ci_to_pass must be true")
	}

	// Project status
	proj, ok := cfg.Coverage.Status.Project["default"]
	if !ok {
		return errors.New("missing coverage.status.project.default")
	}
	if proj.Target != "auto" {
		return fmt.Errorf("coverage.status.project.default.target must be auto, got %q", proj.Target)
	}
	if proj.Threshold != "0%" {
		return fmt.Errorf("coverage.status.project.default.threshold must be 0%%, got %q", proj.Threshold)
	}
	if !slices.Contains(proj.Flags, "unified") {
		return errors.New("coverage.status.project.default.flags must contain unified")
	}
	if proj.Base != "" {
		return fmt.Errorf("deprecated base: %q must not be set on project status", proj.Base)
	}
	if proj.Informational {
		return errors.New("informational: true must not be set on project status")
	}

	// Patch status
	patch, ok := cfg.Coverage.Status.Patch["default"]
	if !ok {
		return errors.New("missing coverage.status.patch.default")
	}
	if patch.Target != "95%" {
		return fmt.Errorf("coverage.status.patch.default.target must be 95%%, got %q", patch.Target)
	}
	if patch.Threshold != "0%" {
		return fmt.Errorf("coverage.status.patch.default.threshold must be 0%%, got %q", patch.Threshold)
	}
	if !slices.Contains(patch.Flags, "unified") {
		return errors.New("coverage.status.patch.default.flags must contain unified")
	}
	if patch.Base != "" {
		return fmt.Errorf("deprecated base: %q must not be set on patch status", patch.Base)
	}
	if patch.Informational {
		return errors.New("informational: true must not be set on patch status")
	}

	// Carryforward must be disabled everywhere
	if cfg.FlagManagement.DefaultRules.Carryforward {
		return errors.New("flag_management.default_rules.carryforward must be false")
	}
	expectedFlags := []string{"default-profile", "minimal-profile", "unified"}
	var seenFlags []string
	for _, f := range cfg.FlagManagement.IndividualFlags {
		seenFlags = append(seenFlags, f.Name)
		if f.Carryforward {
			return fmt.Errorf("flag %q must have carryforward: false", f.Name)
		}
	}
	for _, ef := range expectedFlags {
		if !slices.Contains(seenFlags, ef) {
			return fmt.Errorf("missing flag definition for %q", ef)
		}
	}

	// PR comments and checks
	if cfg.Comment.HideProjectCoverage {
		return errors.New("comment.hide_project_coverage must be false")
	}
	if cfg.Comment.ShowCarryforwardFlags {
		return errors.New("comment.show_carryforward_flags must be false when carryforward is disabled")
	}
	for _, sec := range []string{"header", "diff", "flags", "components", "files", "footer"} {
		if !strings.Contains(cfg.Comment.Layout, sec) {
			return fmt.Errorf("comment.layout must contain %q", sec)
		}
	}
	if !cfg.GitHubChecks.Annotations {
		return errors.New("github_checks.annotations must be true")
	}

	// Ensure deprecated base is not in status definitions
	for _, s := range cfg.ComponentManagement.DefaultRules.Statuses {
		if s.Base != "" {
			return fmt.Errorf("component_management.default_rules.statuses must not set deprecated base %q", s.Base)
		}
	}
	for _, s := range cfg.FlagManagement.DefaultRules.Statuses {
		if s.Base != "" {
			return fmt.Errorf("flag_management.default_rules.statuses must not set deprecated base %q", s.Base)
		}
	}
	return nil
}

func TestCodecovConfigurationContract(t *testing.T) {
	t.Parallel()
	cfg := readCodecovConfig(t)
	require.NoError(t, validateCodecovConfig(cfg))
	assert.Equal(t, 2, cfg.Coverage.Precision)
	assert.Equal(t, "down", cfg.Coverage.Round)
	assert.Equal(t, "70...100", cfg.Coverage.Range)
}

func TestCodecovConfigurationNegativeContracts(t *testing.T) {
	t.Parallel()
	baseCfg := readCodecovConfig(t)

	t.Run("rejects re-enabled default carryforward", func(t *testing.T) {
		t.Parallel()
		mut := baseCfg
		mut.FlagManagement.DefaultRules.Carryforward = true
		require.ErrorContains(t, validateCodecovConfig(mut), "default_rules.carryforward must be false")
	})

	t.Run("rejects re-enabled individual flag carryforward", func(t *testing.T) {
		t.Parallel()
		mut := baseCfg
		mut.FlagManagement.IndividualFlags = slices.Clone(baseCfg.FlagManagement.IndividualFlags)
		mut.FlagManagement.IndividualFlags[0].Carryforward = true
		require.ErrorContains(t, validateCodecovConfig(mut), "must have carryforward: false")
	})

	t.Run("rejects deprecated base on project", func(t *testing.T) {
		t.Parallel()
		mut := baseCfg
		def := mut.Coverage.Status.Project["default"]
		def.Base = "auto"
		mut.Coverage.Status.Project = map[string]struct {
			Target        string   `yaml:"target"`
			Threshold     string   `yaml:"threshold"`
			Base          string   `yaml:"base"`
			Flags         []string `yaml:"flags"`
			Informational bool     `yaml:"informational"`
		}{"default": def}
		require.ErrorContains(t, validateCodecovConfig(mut), "deprecated base")
	})

	t.Run("rejects informational on patch", func(t *testing.T) {
		t.Parallel()
		mut := baseCfg
		def := mut.Coverage.Status.Patch["default"]
		def.Informational = true
		mut.Coverage.Status.Patch = map[string]struct {
			Target        string   `yaml:"target"`
			Threshold     string   `yaml:"threshold"`
			Base          string   `yaml:"base"`
			Flags         []string `yaml:"flags"`
			Informational bool     `yaml:"informational"`
		}{"default": def}
		require.ErrorContains(t, validateCodecovConfig(mut), "informational: true must not be set")
	})

	t.Run("rejects lowered patch target", func(t *testing.T) {
		t.Parallel()
		mut := baseCfg
		def := mut.Coverage.Status.Patch["default"]
		def.Target = "80%"
		mut.Coverage.Status.Patch = map[string]struct {
			Target        string   `yaml:"target"`
			Threshold     string   `yaml:"threshold"`
			Base          string   `yaml:"base"`
			Flags         []string `yaml:"flags"`
			Informational bool     `yaml:"informational"`
		}{"default": def}
		require.ErrorContains(t, validateCodecovConfig(mut), "patch.default.target must be 95%")
	})

	t.Run("rejects missing unified flag filter on project", func(t *testing.T) {
		t.Parallel()
		mut := baseCfg
		def := mut.Coverage.Status.Project["default"]
		def.Flags = nil
		mut.Coverage.Status.Project = map[string]struct {
			Target        string   `yaml:"target"`
			Threshold     string   `yaml:"threshold"`
			Base          string   `yaml:"base"`
			Flags         []string `yaml:"flags"`
			Informational bool     `yaml:"informational"`
		}{"default": def}
		require.ErrorContains(t, validateCodecovConfig(mut), "must contain unified")
	})

	t.Run("rejects hiding project coverage in comments", func(t *testing.T) {
		t.Parallel()
		mut := baseCfg
		mut.Comment.HideProjectCoverage = true
		require.ErrorContains(t, validateCodecovConfig(mut), "hide_project_coverage must be false")
	})
}

func TestCodecovComponentsTargetValidPaths(t *testing.T) {
	t.Parallel()
	cfg := readCodecovConfig(t)
	repoRoot := filepath.Join("..", "..")

	require.NotEmpty(t, cfg.ComponentManagement.IndividualComponents)
	for _, comp := range cfg.ComponentManagement.IndividualComponents {
		t.Run(comp.ComponentID, func(t *testing.T) {
			t.Parallel()
			assert.NotEmpty(t, comp.Name)
			assert.NotEmpty(t, comp.Paths, "component %s must declare at least one path", comp.ComponentID)

			for _, pathPattern := range comp.Paths {
				// Strip wildcard suffix to find root directory
				baseDir := strings.TrimSuffix(pathPattern, "/**")
				baseDir = strings.TrimSuffix(baseDir, "/*")
				matches, err := filepath.Glob(filepath.Join(repoRoot, baseDir))
				require.NoError(t, err)
				assert.NotEmpty(t, matches, "component %s path pattern %s must match on filesystem", comp.ComponentID, pathPattern)
			}
		})
	}
}

type expectedUpload struct {
	id         string
	files      string
	flags      string
	reportType string
}

func validateWorkflowCodecovSteps(job workflowJob) error {
	// Job-level CLI version pin
	cliVersion := job.Env["CODECOV_CLI_VERSION"]
	if cliVersion == "" || cliVersion == "latest" {
		return errors.New("job must pin CODECOV_CLI_VERSION to an exact release, not latest or empty")
	}
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(cliVersion) {
		return fmt.Errorf("job CODECOV_CLI_VERSION must follow semver (e.g. v11.3.1), got %q", cliVersion)
	}

	// Verify required coverage command invocation
	var coverageStep *workflowStep
	var artifactStep *workflowStep
	for i := range job.Steps {
		step := &job.Steps[i]
		if strings.Contains(step.Run, "task coverage-unit") {
			coverageStep = step
		}
		if step.Name == "Verify Coverage & Test Artifacts" {
			artifactStep = step
		}
	}
	if coverageStep == nil {
		return errors.New("workflow missing step running task coverage-unit")
	}
	if !strings.Contains(coverageStep.Run, "task coverage-unit COVERAGE_THRESHOLD=95") {
		return errors.New("coverage step must explicitly specify COVERAGE_THRESHOLD=95")
	}
	if coverageStep.ContinueOnError != nil && *coverageStep.ContinueOnError {
		return errors.New("coverage step must not allow continue-on-error")
	}
	if strings.Contains(coverageStep.Run, "|| true") {
		return errors.New("coverage step must not mask errors with || true")
	}

	if artifactStep == nil {
		return errors.New("workflow missing 'Verify Coverage & Test Artifacts' guard step")
	}
	for _, expectedArtifact := range []string{
		"coverage.out",
		".work/tests/default.out",
		".work/tests/minimal.out",
		".work/tests/junit-default.xml",
		".work/tests/junit-minimal.xml",
	} {
		if !strings.Contains(artifactStep.Run, expectedArtifact) {
			return fmt.Errorf("artifact verification step must check %s", expectedArtifact)
		}
	}

	// Codecov uploads
	commitPin := regexp.MustCompile(`^codecov/codecov-action@[0-9a-f]{40}$`)
	var uploadSteps []workflowStep
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, "codecov/codecov-action") {
			uploadSteps = append(uploadSteps, step)
		}
	}

	const expectedCount = 5
	if len(uploadSteps) != expectedCount {
		return fmt.Errorf("expected exactly %d Codecov upload steps, got %d", expectedCount, len(uploadSteps))
	}

	expectedUploads := []expectedUpload{
		{id: "codecov_unified", files: "coverage.out", flags: "unified", reportType: ""},
		{id: "codecov_default", files: ".work/tests/default.out", flags: "default-profile", reportType: ""},
		{id: "codecov_minimal", files: ".work/tests/minimal.out", flags: "minimal-profile", reportType: ""},
		{id: "codecov_tests_default", files: ".work/tests/junit-default.xml", flags: "default-profile", reportType: "test_results"},
		{id: "codecov_tests_minimal", files: ".work/tests/junit-minimal.xml", flags: "minimal-profile", reportType: "test_results"},
	}

	for i, expected := range expectedUploads {
		step := uploadSteps[i]
		if step.ID != expected.id {
			return fmt.Errorf("step %d ID must be %q, got %q", i, expected.id, step.ID)
		}
		if !commitPin.MatchString(step.Uses) {
			return fmt.Errorf("step %s must use 40-char commit SHA pin, got %q", step.ID, step.Uses)
		}
		if fmt.Sprint(step.With["token"]) != "${{ secrets.CODECOV_TOKEN }}" {
			return fmt.Errorf("step %s must wire secrets.CODECOV_TOKEN", step.ID)
		}
		if step.With["fail_ci_if_error"] != false {
			return fmt.Errorf("step %s must set fail_ci_if_error: false", step.ID)
		}
		if step.With["disable_search"] != true {
			return fmt.Errorf("step %s must set disable_search: true for exclusive file selection", step.ID)
		}
		if fmt.Sprint(step.With["version"]) != "${{ env.CODECOV_CLI_VERSION }}" {
			return fmt.Errorf("step %s must set version: ${{ env.CODECOV_CLI_VERSION }}", step.ID)
		}
		if step.ContinueOnError == nil || !*step.ContinueOnError {
			return fmt.Errorf("step %s must set continue-on-error: true for telemetry error bounding", step.ID)
		}
		const maxStepTimeout = 5
		if step.TimeoutMinutes <= 0 || step.TimeoutMinutes > maxStepTimeout {
			return fmt.Errorf("step %s timeout-minutes must be between 1 and %d, got %d", step.ID, maxStepTimeout, step.TimeoutMinutes)
		}
		if fmt.Sprint(step.With["files"]) != expected.files {
			return fmt.Errorf("step %s expected files %q, got %q", step.ID, expected.files, step.With["files"])
		}
		if fmt.Sprint(step.With["flags"]) != expected.flags {
			return fmt.Errorf("step %s expected flags %q, got %q", step.ID, expected.flags, step.With["flags"])
		}
		if expected.reportType != "" && fmt.Sprint(step.With["report_type"]) != expected.reportType {
			return fmt.Errorf("step %s expected report_type %q, got %q", step.ID, expected.reportType, step.With["report_type"])
		}
		expectedGuard := fmt.Sprintf("hashFiles('%s') != ''", expected.files)
		if !strings.Contains(step.If, "!cancelled()") || !strings.Contains(step.If, expectedGuard) {
			return fmt.Errorf("step %s must guard with !cancelled() and %s, got if: %q", step.ID, expectedGuard, step.If)
		}
	}
	return nil
}

func TestCodecovWorkflowStepIntegrity(t *testing.T) {
	t.Parallel()
	w := readWorkflow(t, "ci.yml")
	testJob, ok := w.Jobs["test"]
	require.True(t, ok)
	require.NoError(t, validateWorkflowCodecovSteps(testJob))
}

func TestCodecovWorkflowNegativeContracts(t *testing.T) {
	t.Parallel()
	w := readWorkflow(t, "ci.yml")
	baseJob, ok := w.Jobs["test"]
	require.True(t, ok)

	t.Run("rejects removed disable_search", func(t *testing.T) {
		t.Parallel()
		mut := baseJob
		mut.Steps = slices.Clone(baseJob.Steps)
		for i, s := range mut.Steps {
			if s.ID == "codecov_unified" {
				stepMut := s
				stepMut.With = mapsClone(s.With)
				delete(stepMut.With, "disable_search")
				mut.Steps[i] = stepMut
				break
			}
		}
		require.ErrorContains(t, validateWorkflowCodecovSteps(mut), "disable_search: true")
	})

	t.Run("rejects swapped paths", func(t *testing.T) {
		t.Parallel()
		mut := baseJob
		mut.Steps = slices.Clone(baseJob.Steps)
		for i, s := range mut.Steps {
			if s.ID == "codecov_unified" {
				stepMut := s
				stepMut.With = mapsClone(s.With)
				stepMut.With["files"] = ".work/tests/default.out"
				mut.Steps[i] = stepMut
				break
			}
		}
		require.ErrorContains(t, validateWorkflowCodecovSteps(mut), "expected files")
	})

	t.Run("rejects missing upload", func(t *testing.T) {
		t.Parallel()
		mut := baseJob
		mut.Steps = nil
		for _, s := range baseJob.Steps {
			if s.ID != "codecov_tests_minimal" {
				mut.Steps = append(mut.Steps, s)
			}
		}
		require.ErrorContains(t, validateWorkflowCodecovSteps(mut), "expected exactly 5 Codecov upload steps")
	})

	t.Run("rejects unpinned CLI", func(t *testing.T) {
		t.Parallel()
		mut := baseJob
		mut.Env = mapsCloneString(baseJob.Env)
		mut.Env["CODECOV_CLI_VERSION"] = "latest"
		require.ErrorContains(t, validateWorkflowCodecovSteps(mut), "not latest or empty")
	})

	t.Run("rejects lowered CI threshold", func(t *testing.T) {
		t.Parallel()
		mut := baseJob
		mut.Steps = slices.Clone(baseJob.Steps)
		for i, s := range mut.Steps {
			if strings.Contains(s.Run, "task coverage-unit") {
				stepMut := s
				stepMut.Run = strings.ReplaceAll(s.Run, "COVERAGE_THRESHOLD=95", "COVERAGE_THRESHOLD=90")
				mut.Steps[i] = stepMut
				break
			}
		}
		require.ErrorContains(t, validateWorkflowCodecovSteps(mut), "COVERAGE_THRESHOLD=95")
	})

	t.Run("rejects tolerated coverage-step failure", func(t *testing.T) {
		t.Parallel()
		mut := baseJob
		mut.Steps = slices.Clone(baseJob.Steps)
		for i, s := range mut.Steps {
			if strings.Contains(s.Run, "task coverage-unit") {
				stepMut := s
				yes := true
				stepMut.ContinueOnError = &yes
				mut.Steps[i] = stepMut
				break
			}
		}
		require.ErrorContains(t, validateWorkflowCodecovSteps(mut), "must not allow continue-on-error")
	})

	t.Run("rejects missing artifact verification guard", func(t *testing.T) {
		t.Parallel()
		mut := baseJob
		mut.Steps = nil
		for _, s := range baseJob.Steps {
			if s.Name != "Verify Coverage & Test Artifacts" {
				mut.Steps = append(mut.Steps, s)
			}
		}
		require.ErrorContains(t, validateWorkflowCodecovSteps(mut), "Verify Coverage & Test Artifacts")
	})
}

func mapsClone[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return nil
	}
	r := make(map[K]V, len(m))
	for k, v := range m {
		r[k] = v
	}
	return r
}

func mapsCloneString(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	r := make(map[string]string, len(m))
	for k, v := range m {
		r[k] = v
	}
	return r
}
