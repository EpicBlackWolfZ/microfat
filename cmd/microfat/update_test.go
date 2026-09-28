package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/update"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const updateCommandName = "update"

type updateWriter struct{ remaining int }

func (w *updateWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, io.ErrClosedPipe
	}
	w.remaining--
	return len(p), nil
}

func TestUpdateCommandContract(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"json", "human", "operation-error", "json-output-error", "human-output-error", "post-activation-output",
		"positional", "flag", "invalid-version", "root-version"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			current, target, management := "0.3.0", "0.3.1", "unmanaged"
			var calls int
			cmd := newUpdateCommand(func(_ context.Context, opts update.Options) (update.Result, error) {
				calls++
				assert.True(t, opts.Check)
				result := update.Result{SchemaVersion: 1, Status: "update_available", RunningVersion: &current,
					CurrentVersion: &current, TargetVersion: &target,
					UpdateAvailable: true, Management: &management, Verification: "metadata_only", Guidance: "upgrade guidance"}
				if scenario == "operation-error" {
					result.Status = "error"
					result.Error = "network failed"
					return result, errors.New(result.Error)
				}
				if scenario == "post-activation-output" {
					result.Activated = true
					result.CurrentVersion = &target
				}
				return result, nil
			})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			args := []string{"--check"}
			switch scenario {
			case "json", "operation-error", "json-output-error":
				args = append(args, "--json")
			case "positional":
				args = append(args, "unexpected")
			case "flag":
				args = append(args, "--bad-option")
			case "invalid-version":
				args = append(args, "--version", "0.3.0-rc1")
			case "root-version":
				root := newRootCmd()
				root.SetOut(&stdout)
				root.SetErr(&stderr)
				root.SetArgs([]string{updateCommandName, "--help"})
				require.NoError(t, root.Execute())
				assert.Contains(t, stdout.String(), "--version string")
				return
			}
			if strings.Contains(scenario, "output") {
				cmd.SetOut(&updateWriter{})
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			invalid := scenario == "positional" || scenario == "flag" || scenario == "invalid-version"
			if invalid {
				var typed updateCommandError
				require.ErrorAs(t, err, &typed)
				assert.Equal(t, updateUsageCode, typed.code)
				assert.Zero(t, calls)
				return
			}
			assert.Equal(t, 1, calls)
			if scenario == "json" || scenario == "human" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if scenario == "json" || scenario == "operation-error" {
				var result update.Result
				require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
				assert.Equal(t, "0.3.0", *result.CurrentVersion)
			}
			if scenario == "human" {
				assert.Contains(t, stdout.String(), "artifact authentication occurs during an update")
			}
			if scenario == "post-activation-output" {
				assert.Contains(t, err.Error(), "v0.3.1 is active")
			}
		})
	}
}

func TestUpdateHumanOutputFailures(t *testing.T) {
	t.Parallel()
	current := "0.3.0"
	result := update.Result{CurrentVersion: &current, Verification: "metadata_only", Guidance: "guidance"}
	for _, remaining := range []int{0, 1, 2} {
		cmd := newUpdateCmd()
		cmd.SetOut(&updateWriter{remaining: remaining})
		require.ErrorIs(t, writeUpdateResult(cmd, result), io.ErrClosedPipe)
	}
}

func TestUpdateMainProcess(t *testing.T) {
	if os.Getenv("MICROFAT_UPDATE_MAIN_CHILD") == "1" {
		os.Args = append([]string{"microfat"}, strings.Split(os.Getenv("MICROFAT_UPDATE_MAIN_ARGS"), "\n")...)
		main()
		return
	}
	t.Parallel()
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, test := range []struct {
		name string
		args []string
		code int
		json bool
	}{
		{"bad-flag", []string{updateCommandName, "--invalid"}, 2, false},
		{"positional", []string{updateCommandName, "unexpected"}, 2, false},
		{"prerelease", []string{updateCommandName, "--version", "0.3.0-rc1"}, 2, false},
		{"unknown-build", []string{updateCommandName, "--check", "--json"}, 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), executable, "-test.run=^TestUpdateMainProcess$")
			command.Env = append(os.Environ(), "MICROFAT_UPDATE_MAIN_CHILD=1", "MICROFAT_UPDATE_MAIN_ARGS="+strings.Join(test.args, "\n"))
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			err := command.Run()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit)
			assert.Equal(t, test.code, exit.ExitCode())
			assert.Contains(t, stderr.String(), "microfat update:")
			if test.json {
				var result update.Result
				require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
				assert.Equal(t, "error", result.Status)
			} else {
				assert.Empty(t, stdout.String())
			}
			assert.NotContains(t, stderr.String(), "Usage:")
		})
	}
}
