package runner

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestHardwareQualifications(t *testing.T) {
	t.Parallel()
	cfg := DefaultExperimentConfig()
	cfg.Target.Affinity, cfg.Generator.Affinity = []int{1}, []int{3}
	assert.Len(t, hostQualifications(nil, cfg), 2)
	host := map[string]string{"/sys/cpu1/cpufreq/scaling_governor": "performance", "/sys/cpu1/topology/thread_siblings_list": "1-2"}
	assert.Empty(t, hostQualifications(host, cfg))
	cfg.Generator.Affinity = []int{2}
	assert.Len(t, hostQualifications(host, cfg), 1)
	host["/sys/cpu1/cpufreq/scaling_governor"] = "powersave"
	assert.Len(t, hostQualifications(host, cfg), 2)
	for _, value := range []string{"", "bad", "2-bad", "4-2", "3,4"} {
		assert.False(t, cpuInList(2, value))
	}
	assert.True(t, cpuInList(2, "1, 2"))
}
