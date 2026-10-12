//go:build linux

package main

import (
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorFiniteZeroMemory(t *testing.T) {
	for _, boundary := range []string{"memory.max", "memory.high"} {
		t.Run(boundary, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{"memory.max": "536870912", "memory.high": "max", "cpu.max": "200000 100000"}
			files[boundary] = "0"
			for name, contents := range files {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600))
			}
			procFile := filepath.Join(t.TempDir(), "cgroup")
			require.NoError(t, os.WriteFile(procFile, []byte("0::/\n"), 0o600))
			oldRead := readCgroupLimitsFunc
			t.Cleanup(func() { readCgroupLimitsFunc = oldRead })
			readCgroupLimitsFunc = func() (cgroup.Limits, error) { return cgroup.ReadLimitsCustom(root, procFile) }
			report := probeCgroup()
			require.True(t, report.Detected)
			assert.True(t, report.MemoryLimitFinite)
			assert.Equal(t, boundary == "memory.high", report.MemoryHighFinite)
			assert.Equal(t, "0B", report.GOMEMLIMITStr)
			assert.Equal(t, 2, report.GOMAXPROCS)
			var output strings.Builder
			printCgroupSection(&output, report)
			assert.NotContains(t, output.String(), "unlimited")
			assert.Contains(t, output.String(), "0 B")
			encoded, err := json.Marshal(report)
			require.NoError(t, err)
			var roundtrip CgroupReport
			require.NoError(t, json.Unmarshal(encoded, &roundtrip))
			assert.Equal(t, *report, roundtrip)
		})
	}
}
