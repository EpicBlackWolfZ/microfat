package cigate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeQualificationIsRequiredAndRetainsEvidence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		workflow, job string
		candidate     bool
	}{
		{"ci.yml", "runtime", false}, {"runtime-qualification.yml", "qualify", true},
	} {
		t.Run(tc.workflow, func(t *testing.T) {
			w := readWorkflow(t, tc.workflow)
			job := w.Jobs[tc.job]
			assert.ElementsMatch(t, []string{"amd64", "arm64"}, job.Strategy.Matrix.Arch)
			require.NotNil(t, job.Strategy.FailFast)
			assert.False(t, *job.Strategy.FailFast)
			assert.Equal(t, 45, job.Timeout)
			qualified, retained := false, false
			for _, step := range job.Steps {
				if strings.Contains(step.Run, "task qualify-runtime") {
					qualified = true
					assert.Contains(t, step.Run, "RUNTIME_TESTS=required")
					assert.Contains(t, step.Run, "RUNTIME_BACKEND=sudo")
					assert.Empty(t, step.If)
					assert.Nil(t, step.ContinueOnError)
					if tc.candidate {
						for _, flag := range []string{"RUNTIME_INPUT=candidate", "RUNTIME_DIST=", "RUNTIME_TAG=", "RUNTIME_SOURCE="} {
							assert.Contains(t, step.Run, flag)
						}
					}
				}
				if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
					retained = true
					assert.Equal(t, "always()", step.If)
					assert.Equal(t, "error", step.With["if-no-files-found"])
					assert.Equal(t, true, step.With["include-hidden-files"])
					assert.Equal(t, 30, step.With["retention-days"])
					assert.Contains(t, step.With["name"], "github.run_attempt")
				}
			}
			assert.True(t, qualified)
			assert.True(t, retained)
			if tc.candidate {
				assert.Equal(t, map[string]string{"contents": "read"}, w.Permissions)
			}
		})
	}
	call := readWorkflow(t, "release.yml").Jobs["runtime-qualification"]
	assert.ElementsMatch(t, []string{"gate", "goreleaser"}, needs(t, call))
	assert.Equal(t, "needs.gate.outputs.runtime-required == 'true'", call.If)
	assert.Equal(t, "./.github/workflows/runtime-qualification.yml", call.Uses)
}

func TestSignedDraftTransfersAcrossThePermissionBoundary(t *testing.T) {
	t.Parallel()
	release := readWorkflow(t, "release.yml")
	producer := release.Jobs["goreleaser"]
	require.Equal(t, "write", producer.Permissions["contents"], "draft acquisition requires push access")
	const artifact = "signed-candidate-${{ github.run_id }}-${{ github.run_attempt }}"
	acquired, uploaded := false, false
	for _, step := range producer.Steps {
		if strings.Contains(step.Run, "gh release download") {
			acquired = true
			assert.Equal(t, "${{ github.token }}", step.Env["GH_TOKEN"])
			assert.Contains(t, step.Run, "release.json")
			assert.Contains(t, step.Run, "--pattern 'microfat_*' --pattern 'microfat-install_*' --pattern 'checksums.txt*'")
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") && step.With["name"] == artifact {
			uploaded = true
			assert.True(t, acquired, "the artifact must contain downloaded draft bytes and identities")
			assert.Equal(t, ".work/signed-candidate/", step.With["path"])
			assert.Equal(t, "error", step.With["if-no-files-found"])
		}
	}
	require.True(t, acquired)
	require.True(t, uploaded)
	assert.Equal(t, map[string]string{"contents": "read"}, release.Jobs["runtime-qualification"].Permissions)
	for _, tc := range []struct{ workflow, job, path string }{
		{"runtime-qualification.yml", "qualify", ".work/runtime-dist"},
		{"release.yml", "installer-qualification", ".work/draft"},
	} {
		t.Run(tc.job, func(t *testing.T) {
			w := readWorkflow(t, tc.workflow)
			job := w.Jobs[tc.job]
			assert.Equal(t, "read", w.Permissions["contents"])
			assert.Empty(t, job.Permissions)
			downloaded, authenticated := false, false
			for _, step := range job.Steps {
				assert.NotContains(t, step.Env, "GH_TOKEN")
				assert.NotContains(t, step.Run, "gh release")
				if strings.HasPrefix(step.Uses, "actions/download-artifact@") {
					downloaded = true
					assert.Equal(t, artifact, step.With["name"])
					assert.Equal(t, tc.path, step.With["path"])
				}
				if strings.Contains(step.Run, "cosign verify-blob") || strings.Contains(step.Run, "RUNTIME_INPUT=candidate") {
					authenticated = true
					assert.True(t, downloaded, "qualification must receive signed bytes before authenticating")
				}
			}
			assert.True(t, downloaded)
			assert.True(t, authenticated)
		})
	}
}
