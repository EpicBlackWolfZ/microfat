package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveVariantPGO_ExactQuotedPaths(t *testing.T) {
	t.Parallel()

	const profileName = " \tprofile.pgo \t"
	tests := []struct {
		name          string
		variant       bool
		absolute      bool
		profileExists bool
	}{
		{name: "default_relative", profileExists: true},
		{name: "default_absolute", absolute: true, profileExists: true},
		{name: "variant_relative", variant: true, profileExists: true},
		{name: "variant_absolute", variant: true, absolute: true, profileExists: true},
		{name: "default_missing_exact_path"},
		{name: "variant_missing_exact_path", variant: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			profilePath := filepath.Join(dir, profileName)
			// A different file at the trimmed name must never be selected.
			require.NoError(t, os.WriteFile(filepath.Join(dir, strings.TrimSpace(profileName)), []byte("wrong profile"), 0o600))
			if tc.profileExists {
				require.NoError(t, os.WriteFile(profilePath, []byte("exact profile"), 0o600))
			}
			pathValue := profileName
			if tc.absolute {
				pathValue = profilePath
			}
			key := "default_pgo"
			if tc.variant {
				key = "pgo"
			}
			manifest := fmt.Sprintf("default_pgo: %q\nvariants:\n  - level: v1\n", pathValue)
			if tc.variant {
				manifest = fmt.Sprintf("default_pgo: off\nvariants:\n  - level: v1\n    pgo: %q\n", pathValue)
			}
			m, err := ParseManifest([]byte(manifest), ".yaml")
			require.NoError(t, err)
			m.Dir = dir
			require.NoError(t, ValidateManifest(m))
			pgoMap, err := resolveAllVariantPGOs(m)
			if !tc.profileExists {
				require.ErrorIs(t, err, ErrProfileNotFound)
				assert.Contains(t, err.Error(), profilePath)
				return
			}
			require.NoError(t, err, "quoted %s path must resolve exactly", key)
			expectedFlag := "-pgo=" + profilePath
			assert.Equal(t, expectedFlag, pgoMap["v1"])
			cmd := buildGoCommand(context.Background(), "go", m, m.Variants[0], pgoMap["v1"], "output", nil)
			assert.Contains(t, cmd.Args, expectedFlag, "Go must receive the exact path in one argument")
		})
	}
}

func TestResolveVariantPGO_OffAndEmptyValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		variantPGO   string
		defaultPGO   string
		expectedFile string
	}{
		{name: "variant_off", variantPGO: "OFF", defaultPGO: "missing"},
		{name: "default_off", defaultPGO: "Off"},
		{name: "empty_uses_package_default", expectedFile: defaultPGOFile},
		{name: "whitespace_variant_is_a_path", variantPGO: " \t", defaultPGO: "off", expectedFile: " \t"},
		{name: "whitespace_default_is_a_path", defaultPGO: " \t", expectedFile: " \t"},
		{name: "padded_variant_off_is_a_path", variantPGO: " off ", defaultPGO: "off", expectedFile: " off "},
		{name: "padded_default_off_is_a_path", defaultPGO: " off ", expectedFile: " off "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			expectedFlag := pgoOffFlag
			if tc.expectedFile != "" {
				profilePath := filepath.Join(dir, tc.expectedFile)
				require.NoError(t, os.WriteFile(profilePath, nil, 0o600))
				expectedFlag = "-pgo=" + profilePath
			}
			flag, err := resolveVariantPGO(VariantConfig{Level: "v1", PGO: tc.variantPGO}, tc.defaultPGO, dir, dir)
			require.NoError(t, err)
			assert.Equal(t, expectedFlag, flag)
		})
	}
}
