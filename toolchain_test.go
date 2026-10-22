package microfat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGoVersionUsesModuleDirective(t *testing.T) {
	t.Parallel()
	require.NotEmpty(t, GoVersion(), "embedded go.mod must declare a Go version")
	for _, tc := range []struct {
		name, module, want string
	}{
		{"directive", "module example.com/fixture\n\ngo 1.27.0\n", "1.27.0"},
		{"whitespace", "go\t1.27.1\r\n", "1.27.1"},
		{"comment", "// go 1.26.0\ngo 1.27.0 // pinned toolchain\n", "1.27.0"},
		{"missing", "module example.com/fixture\n", ""},
		{"malformed", "go\n", ""},
		{"other directive", "toolchain go1.27.1\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, goDirective(tc.module))
		})
	}
}
