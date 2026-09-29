package homebrew

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixtureRecipe(t *testing.T, version string) []byte {
	t.Helper()
	data, err := Render(Recipe{Version: version, AMD64: strings.Repeat("a", 64), ARM64: strings.Repeat("b", 64)})
	require.NoError(t, err)
	return data
}

func TestRecipeContract(t *testing.T) {
	t.Parallel()
	data := fixtureRecipe(t, "0.3.0")
	assert.Equal(t, data, fixtureRecipe(t, "0.3.0"))
	for _, required := range []string{`cask "microfat"`, `depends_on :linux`, `arch: [:x86_64, :arm64]`,
		`binary "microfat"`, `binary "microfat-stub"`, `binary "microfat-stub-minimal"`,
		`"owner":"homebrew"`, `set_permissions "microfat-distribution.json", "0644"`,
		`arm64_linux:`, `x86_64_linux:`, strings.Repeat("a", 64), strings.Repeat("b", 64)} {
		assert.Contains(t, string(data), required)
	}
	for _, forbidden := range []string{"strip", "patchelf", "system_command", ":no_check", "darwin", "install.sh"} {
		assert.NotContains(t, string(data), forbidden)
	}
	for _, version := range []string{"0.2.5", "0.3.0-rc1", "0.3.0\"", "0.03.0", "../../bad", ""} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			_, err := Render(Recipe{version, strings.Repeat("a", 64), strings.Repeat("b", 64)})
			require.Error(t, err)
		})
	}
	_, err := Version("0.3.0")
	require.Error(t, err)
	_, err = Render(Recipe{Version: "0.3.0", AMD64: "bad"})
	require.Error(t, err)
	_, err = CaskVersion(append(data, data...))
	require.Error(t, err)
	assert.Len(t, digest(data), 64)
}

func TestUpdatePolicy(t *testing.T) {
	t.Parallel()
	current, newer := fixtureRecipe(t, "0.3.0"), fixtureRecipe(t, "0.3.1")
	for _, test := range []struct {
		name          string
		current, next []byte
		needed, fails bool
	}{
		{"first", nil, current, true, false}, {"same", current, current, false, false},
		{"upgrade", current, newer, true, false}, {"downgrade", newer, current, false, true},
		{"same changed", append([]byte("# human change\n"), current...), current, false, true},
		{"invalid current", []byte("bad"), current, false, true}, {"invalid candidate", current, nil, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			needed, err := UpdateNeeded(test.current, test.next)
			assert.Equal(t, test.needed, needed)
			assert.Equal(t, test.fails, err != nil)
		})
	}
}
