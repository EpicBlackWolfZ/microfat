package microfat

import (
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolPins(t *testing.T) {
	t.Parallel()
	require.Empty(t, ToolPin("UNKNOWN"))
	require.NotEmpty(t, ToolPin("TASK_VERSION"))
	for line := range strings.Lines(toolDefinitions) {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.HasSuffix(key, "_VERSION") {
			require.Regexp(t, regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`), value, "%s must be an exact release", key)
		}
	}
	version, commit, ok := strings.Cut(ToolPin("HOMEBREW_SOURCE"), "@")
	require.True(t, ok)
	require.Regexp(t, `^v[0-9]+\.[0-9]+\.[0-9]+$`, version)
	require.Regexp(t, `^[0-9a-f]{40}$`, commit)
	for _, tool := range []string{"COSIGN", "CDXGEN", "SHELLCHECK"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			amd64, digest, ok := strings.Cut(ToolPin(tool+"_LINUX_AMD64"), "@")
			require.True(t, ok)
			require.True(t, strings.HasPrefix(amd64, "v"))
			decoded, err := hex.DecodeString(digest)
			require.NoError(t, err)
			require.Len(t, decoded, 32)
			if tool != "SHELLCHECK" {
				arm64, armDigest, ok := strings.Cut(ToolPin(tool+"_LINUX_ARM64"), "@")
				require.True(t, ok)
				require.Equal(t, amd64, arm64, "platform pins must advance together")
				decoded, err = hex.DecodeString(armDigest)
				require.NoError(t, err)
				require.Len(t, decoded, 32)
			}
		})
	}
}

func TestStandaloneBootstrapPinsMatchSharedTools(t *testing.T) {
	t.Parallel()
	bootstrap, err := os.ReadFile("scripts/install.sh")
	require.NoError(t, err)
	for _, arch := range []string{"amd64", "arm64"} {
		require.Contains(t, string(bootstrap), "readonly cosign_"+arch+"='"+ToolPin("COSIGN_LINUX_"+strings.ToUpper(arch))+"'")
	}
}
