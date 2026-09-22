package cigate

import (
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
				Target    string `yaml:"target"`
				Threshold string `yaml:"threshold"`
			} `yaml:"project"`
			Patch map[string]struct {
				Target    string `yaml:"target"`
				Threshold string `yaml:"threshold"`
			} `yaml:"patch"`
		} `yaml:"status"`
	} `yaml:"coverage"`
	ComponentManagement struct {
		DefaultRules struct {
			Statuses []struct {
				Type   string `yaml:"type"`
				Target string `yaml:"target"`
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
		} `yaml:"default_rules"`
		IndividualFlags []struct {
			Name         string `yaml:"name"`
			Carryforward bool   `yaml:"carryforward"`
		} `yaml:"individual_flags"`
	} `yaml:"flag_management"`
	Comment struct {
		Layout              string `yaml:"layout"`
		Behavior            string `yaml:"behavior"`
		RequireChanges      bool   `yaml:"require_changes"`
		RequireHead         bool   `yaml:"require_head"`
		HideProjectCoverage bool   `yaml:"hide_project_coverage"`
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

func TestCodecovConfigurationContract(t *testing.T) {
	t.Parallel()
	cfg := readCodecovConfig(t)

	assert.True(t, cfg.Codecov.RequireCIToPass, "Codecov must wait for CI to pass before notifications")
	assert.Equal(t, 2, cfg.Coverage.Precision)
	assert.Equal(t, "down", cfg.Coverage.Round)
	assert.Equal(t, "90...100", cfg.Coverage.Range)

	assert.Equal(t, "95%", cfg.Coverage.Status.Project["default"].Target)
	assert.Equal(t, "0%", cfg.Coverage.Status.Project["default"].Threshold)
	assert.Equal(t, "95%", cfg.Coverage.Status.Patch["default"].Target)
	assert.Equal(t, "0%", cfg.Coverage.Status.Patch["default"].Threshold)

	assert.True(t, cfg.FlagManagement.DefaultRules.Carryforward)
	var flagNames []string
	for _, f := range cfg.FlagManagement.IndividualFlags {
		flagNames = append(flagNames, f.Name)
		assert.True(t, f.Carryforward, "flag %s must enable carryforward", f.Name)
	}
	assert.ElementsMatch(t, []string{"default-profile", "minimal-profile", "unified"}, flagNames)

	assert.True(t, cfg.GitHubChecks.Annotations)
	assert.Contains(t, cfg.Comment.Layout, "reach")
	assert.Contains(t, cfg.Comment.Layout, "diff")
	assert.Contains(t, cfg.Comment.Layout, "flags")
	assert.Contains(t, cfg.Comment.Layout, "components")
	assert.Contains(t, cfg.Comment.Layout, "files")
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

func TestCodecovWorkflowStepIntegrity(t *testing.T) {
	t.Parallel()
	w := readWorkflow(t, "ci.yml")
	testJob, ok := w.Jobs["test"]
	require.True(t, ok)

	commitPin := regexp.MustCompile(`^codecov/codecov-action@[0-9a-f]{40}$`)
	var codecovSteps []workflowStep
	for _, step := range testJob.Steps {
		if strings.HasPrefix(step.Uses, "codecov/codecov-action") {
			codecovSteps = append(codecovSteps, step)
			assert.Regexp(t, commitPin, step.Uses, "codecov-action must use exact 40-char commit SHA pin")
			assert.Equal(t, "${{ secrets.CODECOV_TOKEN }}", step.With["token"], "step must wire secrets.CODECOV_TOKEN")
			assert.Equal(t, false, step.With["fail_ci_if_error"], "fail_ci_if_error must be false to avoid external CI outage blockers")
		}
	}

	assert.Len(t, codecovSteps, 4, "must have 4 Codecov steps: default-profile, minimal-profile, unified, and test_results")

	var flagsSeen []string
	var hasTestResults bool
	for _, step := range codecovSteps {
		if reportType, ok := step.With["report_type"]; ok && reportType == "test_results" {
			hasTestResults = true
			continue
		}
		if flag, ok := step.With["flags"]; ok {
			flagsSeen = append(flagsSeen, flag.(string))
		}
	}

	assert.True(t, hasTestResults, "must include a step uploading test_results to Codecov Test Analytics")
	assert.True(t, slices.Contains(flagsSeen, "default-profile"))
	assert.True(t, slices.Contains(flagsSeen, "minimal-profile"))
	assert.True(t, slices.Contains(flagsSeen, "unified"))
}
