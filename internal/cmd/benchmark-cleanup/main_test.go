package main

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(t *testing.T) {
	if os.Getenv("MICROFAT_CLEANUP_MAIN_HELPER") == "1" {
		os.Args = []string{"benchmark-cleanup"}
		main()
		return
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMain$")
	cmd.Env = append(os.Environ(), "MICROFAT_CLEANUP_MAIN_HELPER=1")
	output, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "usage: benchmark-cleanup")
}
