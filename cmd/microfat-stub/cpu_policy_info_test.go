//go:build linux && !minimal

package main

import (
	json "encoding/json/v2"
	"io"
	"os"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintInfoNativeCPUPolicy(t *testing.T) {
	const cpuCount = 4
	t.Setenv(format.EnvCPUPolicy, string(cgroup.CPUPolicyNative))
	oldRead := readCgroupLimitsFunc
	t.Cleanup(func() { readCgroupLimitsFunc = oldRead })
	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{CgroupVersion: cgroup.VersionV2, CPUQuota: cpuCount, CPUs: cpuCount}, nil
	}
	for _, jsonOutput := range []bool{false, true} {
		name := "text"
		if jsonOutput {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			require.NoError(t, err)
			t.Cleanup(func() { _ = reader.Close() })
			t.Cleanup(func() { _ = writer.Close() })
			stdout := os.Stdout
			os.Stdout = writer
			t.Cleanup(func() { os.Stdout = stdout })
			entry := &format.VariantEntry{Level: "v1"}
			require.NoError(t, printInfo(&format.Index{}, microarch.Info{}, entry, microarch.PolicyResult{}, 0, jsonOutput))
			require.NoError(t, writer.Close())
			output, err := io.ReadAll(reader)
			require.NoError(t, err)
			if jsonOutput {
				var info struct {
					Cgroup map[string]any `json:"cgroup"`
				}
				require.NoError(t, json.Unmarshal(output, &info))
				assert.NotContains(t, info.Cgroup, "gomaxprocs")
				assert.Equal(t, float64(cpuCount), info.Cgroup["cpu_quota"])
			} else {
				assert.Contains(t, string(output), "4.00 cores -> native Go CPU policy")
				assert.NotContains(t, string(output), "Auto GOMAXPROCS")
			}
		})
	}
}
