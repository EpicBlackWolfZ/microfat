//go:build linux

package cigate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sentinelGHToken     = "ghs_sentinel_primary_token_value"
	sentinelGitHubToken = "ghs_sentinel_fallback_token_value"
)

func extractBashFunction(t *testing.T, script, name string) string {
	t.Helper()
	header := name + "() {"
	start := strings.Index(script, header)
	require.NotEqual(t, -1, start, "missing bash function %s", name)
	endMarker := "\n}\n"
	end := strings.Index(script[start:], endMarker)
	require.NotEqual(t, -1, end, "unterminated bash function %s", name)
	return script[start : start+end+len(endMarker)]
}

func TestPublicAcceptanceWorkflowTokenBoundaries(t *testing.T) {
	t.Parallel()
	w := readWorkflow(t, "release-public-acceptance.yml")
	assert.Equal(t, map[string]string{"contents": permissionRead}, w.Permissions)

	installerJob := w.Jobs["installer"]
	assert.Empty(t, installerJob.Permissions)
	foundPublicInstaller := false
	for _, step := range installerJob.Steps {
		if strings.Contains(step.Run, "scripts/qualify-public-installer.sh") {
			foundPublicInstaller = true
			assert.NotContains(t, step.Env, "GH_TOKEN", "clean public installer container must stay credential-free")
		}
	}
	assert.True(t, foundPublicInstaller)

	homebrewJob := w.Jobs["homebrew"]
	assert.Empty(t, homebrewJob.Permissions)
	foundPublicHomebrew := false
	for _, step := range homebrewJob.Steps {
		if strings.Contains(step.Run, "scripts/qualify-public-homebrew.sh") {
			foundPublicHomebrew = true
			assert.Equal(t, "${{ github.token }}", step.Env["GH_TOKEN"])
		}
	}
	assert.True(t, foundPublicHomebrew)
}

func TestQualifyInstallerScopesGitHubTokenToUpdaterOnly(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "qualify-installer.sh"))
	require.NoError(t, err)
	script := string(data)

	captureLine := "update_gh_token=${GH_TOKEN:-${GITHUB_TOKEN:-}}"
	unsetLine := "unset GH_TOKEN GITHUB_TOKEN"
	captureIdx := strings.Index(script, captureLine)
	unsetIdx := strings.Index(script, unsetLine)
	curlIdx := strings.Index(script, "curl ")
	require.Greater(t, captureIdx, 0, "qualify-installer.sh must capture optional GH_TOKEN/GITHUB_TOKEN")
	require.Greater(t, unsetIdx, captureIdx, "qualify-installer.sh must unset tokens immediately after capture")
	require.Greater(t, curlIdx, unsetIdx, "tokens must be unset before curl downloads Cosign")
	assert.Contains(t, script, `run_updater "${mode}" --check --version "${previous}" --json`)
	assert.Contains(t, script, `run_updater "${mode}" --version "${previous}" --allow-downgrade`)

	fnBody := extractBashFunction(t, script, "run_updater")
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	fakeMicrofat := filepath.Join(binDir, "microfat")
	writeExecutableTestStub(t, fakeMicrofat,
		"#!/bin/bash\n"+
			"printf 'args=%s|gh=%s|github=%s|mode=%s\\n' "+
			"\"$*\" \"${GH_TOKEN:-}\" \"${GITHUB_TOKEN:-}\" \"${MICROFAT_EXEC_MODE:-}\"\n")

	for _, tc := range []struct {
		name      string
		env       []string
		wantToken string
	}{
		{"primary_gh_token", []string{"GH_TOKEN=" + sentinelGHToken, "GITHUB_TOKEN=" + sentinelGitHubToken}, sentinelGHToken},
		{"fallback_github_token", []string{"GITHUB_TOKEN=" + sentinelGitHubToken}, sentinelGitHubToken},
		{"credential_free", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			harness := filepath.Join(dir, tc.name+".sh")
			body := "#!/usr/bin/env bash\nset -euo pipefail\nIFS=$'\\n\\t'\n" +
				captureLine + "\n" + unsetLine + "\n" +
				"fixture_home=" + dir + "\nprivate=" + dir + "\nbin=" + binDir + "\n" +
				fnBody +
				"printf 'ambient=%s:%s\\n' \"${GH_TOKEN:-}\" \"${GITHUB_TOKEN:-}\"\n" +
				"run_updater memfd --check --version 0.2.5 --json\n"
			require.NoError(t, os.WriteFile(harness, []byte(body), 0o600))
			cmd := exec.CommandContext(t.Context(), "bash", harness)
			cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, tc.env...)
			output, runErr := combinedOutputSafe(cmd)
			require.NoError(t, runErr, "%s", output)
			assert.Contains(t, string(output), "ambient=:\n")
			assert.Contains(t, string(output), "args=update --check --version 0.2.5 --json|gh="+tc.wantToken+"|github=|mode=memfd\n")
		})
	}
}

func TestQualifyPublicHomebrewScopesAndRedactsGitHubToken(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "qualify-public-homebrew.sh"))
	require.NoError(t, err)
	script := string(data)

	captureLine := "update_gh_token=${GH_TOKEN:-${GITHUB_TOKEN:-}}"
	unsetLine := "unset GH_TOKEN GITHUB_TOKEN"
	captureIdx := strings.Index(script, captureLine)
	unsetIdx := strings.Index(script, unsetLine)
	require.Greater(t, captureIdx, 0, "qualify-public-homebrew.sh must capture optional GH_TOKEN/GITHUB_TOKEN")
	require.Greater(t, unsetIdx, captureIdx, "qualify-public-homebrew.sh must unset tokens immediately after capture")
	assert.Contains(t, script, `run_recorded_update_check "update-${phase}-${mode}.json" "${mode}"`)
	assert.Contains(t, script, `update --check --version "${release_version}" --json`)
	assert.NotContains(t, script, `update --check --version 0.2.5`)

	fnBody := extractBashFunction(t, script, "run_recorded_update_check")
	dir := t.TempDir()
	binDir := filepath.Join(dir, "prefix", "bin")
	evidenceDir := filepath.Join(dir, "evidence")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	require.NoError(t, os.MkdirAll(evidenceDir, 0o700))
	writeExecutableTestStub(t, filepath.Join(binDir, "microfat"),
		"#!/bin/bash\n"+
			"printf '%s' \"${GH_TOKEN:-}\" > \"$TEST_OBSERVED_TOKEN\"\n"+
			"printf '{\"management\":\"homebrew\",\"can_self_update\":false}\\n'\n")

	for _, tc := range []struct {
		name      string
		env       []string
		wantToken string
	}{
		{"with_token", []string{"GH_TOKEN=" + sentinelGHToken}, sentinelGHToken},
		{"credential_free", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			subEvidence := filepath.Join(evidenceDir, tc.name)
			require.NoError(t, os.MkdirAll(subEvidence, 0o700))
			tokenFile := filepath.Join(subEvidence, "observed.token")
			harness := filepath.Join(dir, tc.name+".sh")
			body := "#!/usr/bin/env bash\nset -euo pipefail\nIFS=$'\\n\\t'\n" +
				captureLine + "\n" + unsetLine + "\n" +
				"fail() { printf '%s\\n' \"$*\" >&2; exit 1; }\n" +
				"evidence_root=" + subEvidence + "\n" +
				"application_environment=(env -i \"PATH=" + binDir + ":/usr/bin:/bin\" \"TEST_OBSERVED_TOKEN=" + tokenFile + "\")\n" +
				fnBody +
				"run_recorded_update_check update.json memfd update --check --version 0.4.0 --json\n"
			require.NoError(t, os.WriteFile(harness, []byte(body), 0o600))
			cmd := exec.CommandContext(t.Context(), "bash", harness)
			cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, tc.env...)
			output, runErr := combinedOutputSafe(cmd)
			require.NoError(t, runErr, "%s", output)

			observed, readErr := os.ReadFile(tokenFile)
			require.NoError(t, readErr)
			assert.Equal(t, tc.wantToken, string(observed))

			commandsLog, readErr := os.ReadFile(filepath.Join(subEvidence, "commands.log"))
			require.NoError(t, readErr)
			assert.Contains(t, string(commandsLog), "update --check --version 0.4.0 --json")
			assert.NotContains(t, string(commandsLog), "GH_TOKEN")
			assert.NotContains(t, string(commandsLog), sentinelGHToken)
		})
	}
}
