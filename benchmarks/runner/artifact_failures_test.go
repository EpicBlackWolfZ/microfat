package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/require"
)

const missingExperimentPath = "/missing"

func fixtureCompiler(t *testing.T, behavior string) string {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "go")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo 'go version go1.27.1 linux/amd64'; exit 0; fi\n" +
		"while [ \"$#\" -gt 0 ]; do if [ \"$1\" = -o ]; then shift; output=$1; fi; shift; done\n" +
		"case \"$output\" in\n" + behavior + "\n*/head-packer) cp /bin/false \"$output\"; exit 0;;\nesac\ncp '" + binary + "' \"$output\"\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

func TestArtifactBuildFailures(t *testing.T) {
	t.Parallel()
	repository, err := filepath.Abs("../..")
	require.NoError(t, err)
	for name, behavior := range map[string]string{
		"native compile": "*/native-*) exit 8;;",
		"native missing": "*/native-*) exit 0;;",
		"stub compile":   "*/stub) exit 8;;",
		"stub missing":   "*/stub) exit 0;;",
		"invalid stub":   "*/stub) echo invalid > \"$output\"; exit 0;;",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultExperimentConfig()
			_, err := buildArtifacts(context.Background(), cfg, RunOptions{Repository: repository, Go: fixtureCompiler(t, behavior)}, t.TempDir())
			require.Error(t, err)
		})
	}
	cfg := DefaultExperimentConfig()
	cfg.SpecializedLevel = "invalid"
	_, err = buildArtifacts(context.Background(), cfg, RunOptions{Repository: repository, Go: "go"}, t.TempDir())
	require.Error(t, err)
	_, err = buildArtifacts(context.Background(), DefaultExperimentConfig(), RunOptions{Repository: t.TempDir(), Go: "go"}, t.TempDir())
	require.Error(t, err)
	require.Error(t, identifyTools(RunOptions{Fortio: missingExperimentPath, Helper: missingExperimentPath}, &builtArtifacts{}))
	for name, behavior := range map[string]string{
		"base stub":      "*/base-stub) exit 8;;",
		"base packer":    "*/base-packer) exit 8;;",
		"base packaging": "*/base-packer) cp /bin/false \"$output\"; exit 0;;",
	} {
		t.Run(name, func(t *testing.T) {
			opts := RunOptions{BaseRepository: repository, Go: fixtureCompiler(t, behavior)}
			require.Error(t, addRevisionArtifacts(context.Background(), DefaultExperimentConfig(), opts, t.TempDir(), "v1",
				map[string]string{"v1": missingExperimentPath}, &builtArtifacts{}))
		})
	}
	require.Error(t, addRevisionArtifacts(context.Background(), DefaultExperimentConfig(), RunOptions{BaseRepository: missingExperimentPath},
		t.TempDir(), "v1", nil, &builtArtifacts{}))
	exp := &schema.ExperimentV2{Schedule: []schema.ScheduledTrial{{ID: "trial", ConfigurationID: "missing"}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, executeSchedule(ctx, DefaultExperimentConfig(), RunOptions{}, t.TempDir(), t.TempDir(), &builtArtifacts{}, exp, nil))
}
