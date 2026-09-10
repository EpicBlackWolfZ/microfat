package env

import (
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
)

func TestRunnerProvenance(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	assert.Equal(t, "local", Runner().Provider)
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("RUNNER_ENVIRONMENT", schema.HostedProvider)
	t.Setenv("ImageVersion", "version")
	t.Setenv("GITHUB_RUN_ID", "123")
	r := Runner()
	assert.Equal(t, schema.HostedProvider, r.Provider)
	assert.Equal(t, "version", r.ImageVersion)
	assert.Equal(t, schema.StartupProtocol, r.Protocol)
	assert.Equal(t, "123", r.RunID)
}
