//go:build linux

package e2e_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLauncherMetaCommandsWithoutSelectedPayload(t *testing.T) {
	t.Parallel()
	levels := make([]string, 0, len(goldenVariantBins))
	for level := range goldenVariantBins {
		levels = append(levels, level)
	}
	for _, profile := range cacheFirstProfiles(t) {
		t.Run(profile.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			env := append(cacheFirstEnv(dir), "MICROFAT_DISABLE_VARIANTS="+strings.Join(levels, ","))
			for _, command := range []string{"--microfat:help", "--microfat:info", stubInfoJSONFlag,
				"--microfat:prewarm=all,json", "--microfat:prewarm=" + levels[0] + ",verify,json"} {
				stdout, stderr, err := runCacheFirstProcess(t, profile.path, env, command)
				if profile.name == launcherMinimalProfile {
					require.Error(t, err)
					require.Contains(t, stderr, "meta-commands are disabled")
					continue
				}
				require.NoError(t, err, stderr)
				require.NotContains(t, stdout, stubInfoPayloadTag)
				if command == stubInfoJSONFlag {
					var report struct {
						SelectedVariant string `json:"selected_variant"`
						SelectionError  string `json:"selection_error"`
						Runtime         struct {
							Candidates []json.RawMessage `json:"candidates"`
						} `json:"runtime"`
					}
					require.NoError(t, json.Unmarshal([]byte(stdout), &report))
					require.Empty(t, report.SelectedVariant)
					require.Contains(t, report.SelectionError, "selecting compatible CPU")
					require.Empty(t, report.Runtime.Candidates)
				}
			}
			for _, command := range []string{"ordinary", "--microfat:trim", "--microfat:optimize", "--microfat:prewarm"} {
				_, stderr, err := runCacheFirstProcess(t, profile.path, env, command)
				require.Error(t, err, command)
				if profile.name == launcherFullProfile && command == "--microfat:prewarm" {
					require.Contains(t, stderr, "prewarm requires all or an explicit variant level")
				}
			}
			if profile.name == launcherMinimalProfile {
				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				require.Empty(t, entries)
			}
		})
	}
}
