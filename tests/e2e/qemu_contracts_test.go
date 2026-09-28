//go:build linux

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
)

func TestQemuOutcomeContracts(t *testing.T) {
	t.Parallel()
	const digest = "expected-image"
	const size = 123
	for _, name := range []string{"limitation", "success", "signal", "started", "no-telemetry", "wrong-digest",
		"wrong-mode", "wrong-size", "malformed", qemuDuplicate, "preparation-error"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run := mountfixture.Execution{ExitCode: 1}
			record := qemuDispatch{Event: "dispatch", Digest: digest, Size: size, Mode: execModeMemfd}
			switch name {
			case "success":
				run.ExitCode = 0
			case "signal":
				run.ExitCode = -1
			case "started":
				run.Started = mountStartup
			case "wrong-digest":
				record.Digest = "wrong"
			case "wrong-mode":
				record.Mode = execModeCache
			case "wrong-size":
				record.Size++
			case "preparation-error":
				record.Event = "error"
			}
			data, err := json.Marshal(record)
			require.NoError(t, err)
			run.Stderr = "[microfat] " + string(data) + "\n"
			switch name {
			case "malformed":
				run.Stderr = "[microfat] not json"
			case "no-telemetry":
				run.Stderr = "arbitrary failure"
			case qemuDuplicate:
				run.Stderr += run.Stderr
			}
			_, err = qemuLimitationResult(run, digest, execModeMemfd, size)
			if name == "limitation" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	for _, stderr := range []string{"", qemuProbePrefix + `{}`, qemuProbePrefix + `invalid`,
		qemuProbePrefix + `{"event":"exec-returned"}`, qemuProbePrefix + `{"event":"before-exec","error":"bad"}`,
		qemuProbePrefix + `{"event":"before-exec"}` + "\n" + qemuProbePrefix + `{"event":"before-exec"}`} {
		_, err := qemuProbeRecord(stderr)
		require.Error(t, err)
	}
	_, err := qemuProbeRecord(qemuProbePrefix + `{"event":"before-exec"}`)
	require.NoError(t, err)
}

func TestQemuEvidenceCompleteness(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"complete", "missing", qemuDuplicate, qemuUnknown, "skip", "failed", "no-outcome", "wrong-outcome"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			summary := qemuSummary{mountSummary: mountSummary{Expected: qemuExpected()}, Outcomes: map[string]string{}}
			for _, id := range summary.Expected {
				summary.Results = append(summary.Results, mountEvidence{Case: id, Status: mountPass})
				summary.Outcomes[id] = qemuExpectedOutcome(id)
			}
			switch name {
			case "missing":
				summary.Results = summary.Results[1:]
			case qemuDuplicate:
				summary.Results[1].Case = summary.Results[0].Case
			case qemuUnknown:
				summary.Results[0].Case = qemuUnknown
			case "skip":
				summary.Results[0].Status = mountSkip
			case "failed":
				summary.Results[0].Status = mountFail
			case "no-outcome":
				delete(summary.Outcomes, summary.Expected[0])
			case "wrong-outcome":
				summary.Outcomes[summary.Expected[0]] = qemuLimitation
			}
			err := validateQemuSummary(summary)
			if name == "complete" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestQemuRegistrationContracts(t *testing.T) {
	t.Parallel()
	const good = "enabled\ninterpreter /emulator\nflags: F\noffset 0\n" +
		"magic 7f454c460201010000000000000000000200b700\nmask ffffffffffffff00fffffffffffffffffeffffff\n"
	for _, raw := range []string{"", strings.ReplaceAll(good, "enabled", "disabled"), strings.ReplaceAll(good, "F", "POF"),
		strings.ReplaceAll(good, "/emulator", "/other"), strings.ReplaceAll(good, "b700", "3e00")} {
		require.Error(t, validateQemuRegistration(raw, "/emulator"))
	}
	require.NoError(t, validateQemuRegistration(good, "/emulator"))
	path := filepath.Join(t.TempDir(), "not-elf")
	require.NoError(t, os.WriteFile(path, []byte("invalid"), privateFilePerm))
	require.Error(t, validateQemuEmulator(path))
	require.Error(t, validateQemuEmulator(path+"missing"))
	if runtime.GOARCH == archAMD64 {
		require.NoError(t, validateQemuEmulator(stubPath))
		data, err := os.ReadFile(stubPath)
		require.NoError(t, err)
		const machineOffset = 18
		data[machineOffset], data[machineOffset+1] = 0xb7, 0
		require.NoError(t, os.WriteFile(path, data, privateFilePerm))
		require.ErrorContains(t, validateQemuEmulator(path), "native amd64")
	}
	// The race-enabled native test executable has an ELF interpreter.
	if runtime.GOARCH == archAMD64 {
		self, err := os.Executable()
		require.NoError(t, err)
		// Non-race go test can be static; exercise dynamic rejection when the
		// current test executable has an interpreter, without requiring race here.
		if err := validateQemuEmulator(self); err != nil {
			require.ErrorContains(t, err, "static")
		}
	}
}

func TestQemuPrivilegedBootstrapRefusesOrdinaryCredentials(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires ordinary credentials")
	}
	require.NoError(t, buildQemuSupervisor())
	out, err := exec.Command(qemuSupervisorPath(), "1000", "1000", "true").CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(out), "requires explicit privileged setup")
}

func TestQemuHarnessContracts(t *testing.T) {
	t.Run("missing-tool", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		reason, err := qemuPrerequisite(qemuUserNS)
		require.NoError(t, err)
		require.NotEmpty(t, reason)
	})
	products := buildMountProducts(t, t.TempDir())
	p := qemuProducts{mountProducts: products, emulator: products.reporter, probe: products.reporter,
		nativeProbe: products.reporter, nativeReporter: products.reporter}
	for _, kind := range []string{"missing-user-namespace", "parent-user-namespace"} {
		t.Run(kind, func(t *testing.T) {
			req := prepareQemuRequest(t, p, products.reporter, execModeNative)
			if kind == "missing-user-namespace" {
				req.Binfmt.ParentUserNamespace = ""
			}
			request := filepath.Join(req.Root, "request.json")
			writeMountJSON(t, request, req)
			out, err := exec.Command(products.controller, request).CombinedOutput()
			require.Error(t, err)
			require.Contains(t, string(out), "namespace")
		})
	}
	if os.Getenv(qemuModeEnv) == "" {
		t.Skip("real isolation contracts require task qualify-qemu")
	}
	backend := os.Getenv(qemuBackendEnv)
	if backend == "" {
		backend = qemuUserNS
	}
	reason, err := qemuPrerequisite(backend)
	require.NoError(t, err)
	if reason != "" {
		if os.Getenv(qemuModeEnv) == mountRequired {
			t.Fatal(reason)
		}
		t.Skip(reason)
	}
	before := qemuParentBinfmt(t)
	t.Cleanup(func() { require.Equal(t, before, qemuParentBinfmt(t)) })
	t.Run("setup-failure", func(t *testing.T) {
		req := prepareQemuRequest(t, p, products.reporter, execModeNative)
		req.Mounts[0].Source = "missing-source"
		request := filepath.Join(req.Root, "request.json")
		writeMountJSON(t, request, req)
		_, result, _, err := invokeMountArgs(qemuNamespaceCommand(backend, products.controller, request), qemuControllerTimeout)
		require.Error(t, err)
		require.Empty(t, result.Prerequisite, "setup errors cannot silently skip")
		require.Empty(t, result.Executions)
	})
}

func TestQemuUnsupportedHostEvidence(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{qemuAuto, mountRequired} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			// Force the platform prerequisite without executing Go or namespace tools.
			shim := filepath.Join(root, "uname")
			require.NoError(t, os.WriteFile(shim, []byte("#!/bin/sh\nprintf 'unsupported\\n'\n"), defaultFilePerm))
			cmd := exec.CommandContext(t.Context(), "bash", "../../scripts/qualify-qemu.sh")
			cmd.Env = []string{"PATH=" + root + ":" + os.Getenv("PATH"), "QEMU_OUTPUT=" + root,
				"QEMU_TESTS=" + mode, "GO=/must/not/execute"}
			out, err := cmd.CombinedOutput()
			if mode == mountRequired {
				require.Error(t, err)
			} else {
				require.NoError(t, err, "%s", out)
			}
			require.Contains(t, string(out), "Incomplete QEMU qualification")
			paths, err := filepath.Glob(filepath.Join(root, "run-*", "summary.json"))
			require.NoError(t, err)
			require.Len(t, paths, 1)
			data, err := os.ReadFile(paths[0])
			require.NoError(t, err)
			var summary qemuSummary
			require.NoError(t, json.Unmarshal(data, &summary))
			require.Equal(t, "incomplete", summary.Status)
			require.Equal(t, qemuExpected(), summary.Expected)
			require.Error(t, validateQemuSummary(summary))
		})
	}
}

func TestQemuPrerequisiteClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, output  string
		err           error
		skip, failure bool
	}{
		{"available", "", nil, false, false},
		{"missing-tool", "", exec.ErrNotFound, true, false},
		{"namespace-denied", "unshare: Operation not permitted", errors.New("exit 1"), true, false},
		{"sudo-denied", "sudo: a password is required", errors.New("exit 1"), true, false},
		{"unknown-failure", "unexpected error", errors.New("exit 1"), false, true},
		{"timeout", "", context.DeadlineExceeded, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reason, err := classifyQemuPrerequisite(tc.output, tc.err)
			require.Equal(t, tc.skip, reason != "")
			require.Equal(t, tc.failure, err != nil)
		})
	}
}
