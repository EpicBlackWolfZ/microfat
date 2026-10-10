//go:build !minimal

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractTargetPathPreservesBytes(t *testing.T) {
	// Separate arguments use process-global os.Args, so these cases run serially.
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	commands := []struct {
		name    string
		flag    string
		primary string
		alias   string
	}{
		{name: "OptimizeTo", flag: flagOptimizeTo, primary: flagOptimizeTo},
		{name: "TrimTo", flag: flagTrimTo, primary: flagTrimTo, alias: flagSpecializeTo},
		{name: "SpecializeTo", flag: flagSpecializeTo, primary: flagTrimTo, alias: flagSpecializeTo},
	}
	paths := []struct {
		name string
		path string
	}{
		{name: "LeadingSpace", path: " output"},
		{name: "TrailingSpace", path: "output "},
		{name: "TrailingTab", path: "output\t"},
		{name: "OnlySpaces", path: "  "},
		{name: "OnlyTab", path: "\t"},
		{name: "LeadingEquals", path: "=output"},
		{name: "Empty", path: ""},
	}
	for _, command := range commands {
		for _, path := range paths {
			for _, form := range []string{"Equals", "Separate"} {
				t.Run(command.name+"/"+path.name+"/"+form, func(t *testing.T) {
					arg := command.flag + "=" + path.path
					os.Args = []string{testAppArg, arg}
					if form == "Separate" {
						arg = command.flag
						os.Args = []string{testAppArg, arg, flagMetadataPolicyPrefix + "strict", path.path}
					}
					got, err := extractTargetPath(arg, command.primary, command.alias)
					if path.path == "" {
						require.ErrorContains(t, err, "requires a destination path")
						require.Empty(t, got)
						return
					}
					require.NoError(t, err)
					require.Equal(t, path.path, got)
				})
			}
		}
	}
}
