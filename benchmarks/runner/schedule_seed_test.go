package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleSeedConfiguration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, config string
		want         uint64
		explicit     bool
	}{
		{name: "absent retains workload seed", config: `{"seed":7}`, want: 7},
		{name: "explicit zero", config: `{"seed":7,"schedule_seed":0}`, explicit: true},
		{name: "independent order", config: `{"seed":7,"schedule_seed":42}`, want: 42, explicit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "suite.json")
			require.NoError(t, os.WriteFile(path, []byte(tc.config), evidenceFilePerm))
			cfg, err := ReadExperimentConfig(path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.scheduleSeed())
			assert.Equal(t, tc.explicit, cfg.ScheduleSeed != nil)
			assert.Equal(t, uint64(7), cfg.Seed, "counterbalancing does not change workload or bootstrap input")
		})
	}
}
