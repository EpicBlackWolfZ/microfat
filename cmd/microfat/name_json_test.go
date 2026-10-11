package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLIPackNameJSONRoundTrip(t *testing.T) {
	t.Parallel()
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, tc := range []struct {
			name, appName string
			valid         bool
		}{
			{"Unicode", "Café 日本語 ★", true},
			{"invalid UTF-8", "application-\xff", false},
		} {
			t.Run(fmt.Sprintf("format%d/%s", version, tc.name), func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				input, output := filepath.Join(dir, "input"), filepath.Join(dir, "output")
				require.NoError(t, os.WriteFile(input, []byte("payload"), 0o755))
				require.NoError(t, os.WriteFile(output, []byte("keep"), 0o600))
				cmd := newPackCmd()
				cmd.SetArgs([]string{
					flagStub, input, flagOutput, output, flagName, tc.appName,
					flagFormatVersion, fmt.Sprint(version), "-v", "v1=" + input,
					"--compression", "none", flagSkipELF,
				})
				err := cmd.Execute()
				if !tc.valid {
					require.ErrorContains(t, err, "valid UTF-8")
					data, readErr := os.ReadFile(output)
					require.NoError(t, readErr)
					assert.Equal(t, "keep", string(data))
					return
				}
				require.NoError(t, err)
				for _, jsonCmd := range []*cobra.Command{newInspectCmd(), newVerifyCmd()} {
					var data bytes.Buffer
					jsonCmd.SetOut(&data)
					jsonCmd.SetArgs([]string{output, flagJSON})
					require.NoError(t, jsonCmd.Execute())
					var parsed map[string]any
					require.NoError(t, json.Unmarshal(data.Bytes(), &parsed))
					assert.Equal(t, tc.appName, parsed["app_name"])
				}
			})
		}
	}
}
