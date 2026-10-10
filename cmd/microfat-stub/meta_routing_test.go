//go:build !minimal

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetaRoutingForwardsApplicationArguments(t *testing.T) {
	// os.Args is process-wide, so these routing tests run serially.
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"ordinary argument", []string{"application", flagMetadataPolicyPrefix + "invalid"}},
		{"application flag", []string{"--echo-args", flagBreakHardlinks, flagMetadataPolicyPrefix + "invalid"}},
		{"sentinel first", []string{"--", flagOptimize, flagMetadataPolicyPrefix + "invalid"}},
		{"sentinel after argument", []string{"application", "--", flagMetadataPolicyPrefix + "invalid"}},
		{"policy first", []string{flagMetadataPolicyPrefix + "invalid"}},
		{"unhandled command", []string{"--microfat:unknown", flagMetadataPolicyPrefix + "invalid"}},
		{"similar command", []string{flagTrim + "-extra", flagMetadataPolicyPrefix + "invalid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = append([]string{"app"}, tc.args...)
			before := append([]string(nil), os.Args...)
			handled, err := handleMetaCommand(os.Args[1], "", nil, 0, nil, microarch.Info{}, nil, microarch.PolicyResult{})
			require.NoError(t, err)
			assert.False(t, handled)
			assert.Equal(t, before, os.Args)
		})
	}
}

func TestMetaRoutingRejectsInvalidTransformationPolicy(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })
	path := filepath.Join(t.TempDir(), "app")
	require.NoError(t, os.WriteFile(path, []byte("original"), 0o700))
	image, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = image.Close() })
	for _, command := range []string{
		flagTrim, flagSpecialize, flagOptimize,
		flagTrimTo, flagSpecializeTo, flagOptimizeTo,
		flagTrimTo + "=destination", flagSpecializeTo + "=destination", flagOptimizeTo + "=destination",
	} {
		t.Run(command, func(t *testing.T) {
			os.Args = []string{"app", command, flagMetadataPolicyPrefix + "invalid"}
			handled, err := handleMetaCommand(command, path, image, 0, nil, microarch.Info{}, nil, microarch.PolicyResult{})
			require.ErrorContains(t, err, "invalid metadata policy")
			assert.True(t, handled)
		})
	}
}
