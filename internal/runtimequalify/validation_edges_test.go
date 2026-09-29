package runtimequalify

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
)

func caseFor(t *testing.T, scenario, mode string) Case {
	t.Helper()
	for _, c := range Cases() {
		if c.Scenario == scenario && c.Mode == mode {
			return c
		}
	}
	t.Fatal("missing case")
	return Case{}
}

func TestEvidenceRejectsControlAndPolicyFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, scenario, mode string
		change               func(*Evidence)
	}{
		{"missing-runtime", observe, Memfd, func(e *Evidence) { e.Request.Runtime = nil }},
		{"wrong-worker-count", observe, Memfd, func(e *Evidence) { e.Request.Runtime.Workers++ }},
		{"wrong-mount", observe, Memfd, func(e *Evidence) { e.Request.Proc = "missing" }},
		{"writer-control", busy, Cache, func(e *Evidence) { e.Request.Runtime.BusyName = "" }},
		{"wrong-copy", corruptPayload, Memfd, func(e *Evidence) { e.ExecutedSHA256 = e.Lineage.BundleSHA256 }},
		{"timeout", observe, Memfd, func(e *Evidence) { e.Result.Executions[0].Timeout = true }},
		{"started-on-denial", createEPERM, Memfd, func(e *Evidence) { e.Result.Executions[0].Started = Startup }},
		{"wrong-failure-stage", createEPERM, Memfd, func(e *Evidence) {
			e.Result.Executions[0].Stderr = strings.ReplaceAll(e.Result.Executions[0].Stderr, "memfd_create", "cache_extract")
		}},
		{"wrong-errno", createEPERM, Memfd, func(e *Evidence) {
			e.Result.Executions[0].Stderr = strings.ReplaceAll(e.Result.Executions[0].Stderr, "EPERM", "ENOSYS")
		}},
		{"unsealed", observe, Memfd, func(e *Evidence) { e.Result.Executions[0].PolicyEvents[1].Immutable = false }},
		{"listener-loss", observe, Memfd, func(e *Evidence) { e.Result.Executions[0].PolicyEvents = nil }},
		{"extra-notification", observe, Memfd, func(e *Evidence) {
			e.Result.Executions[0].PolicyEvents = append(e.Result.Executions[0].PolicyEvents, mountfixture.PolicyEvent{})
		}},
		{"wrong-target", observe, Memfd, func(e *Evidence) { e.Result.Executions[0].PolicyEvents[1].Digest = "substituted" }},
		{"wrong-resource-control", fdPressure, Memfd, func(e *Evidence) { e.Result.Executions[0].PolicyEvents[0].Decision = "emulated" }},
		{"wrong-raw-control", policyControl, Memfd, func(e *Evidence) { e.Result.Executions[0].Stdout = "{}" }},
		{"bad-raw-control", policyControl, Memfd, func(e *Evidence) { e.Result.Executions[0].Stderr = "panic" }},
		{"not-readonly", readOnlyWarm, Cache, func(e *Evidence) { e.Result.CacheSnapshots[0].Flags = 0 }},
		{"not-noexec", noExec, Cache, func(e *Evidence) { e.Result.CacheSnapshots[0].Flags = 0 }},
		{"not-private-tmpfs", Full, Cache, func(e *Evidence) { e.Result.CacheSnapshots[0].Filesystem = 1 }},
		{"signal-lost", "signal", Cache, func(e *Evidence) { e.Result.Executions[0].Signal = "" }},
		{"too-many-cancellations", observe, Memfd, func(e *Evidence) {
			for range 129 {
				e.Result.Executions[0].PolicyEvents = append(e.Result.Executions[0].PolicyEvents,
					mountfixture.PolicyEvent{Decision: "cancelled"})
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := caseFor(t, tc.scenario, tc.mode)
			if tc.name == "wrong-raw-control" {
				c = Cases()[1]
			}
			e := validEvidence(t, c)
			tc.change(&e)
			require.Error(t, ValidateEvidence(e))
		})
	}
	e := validEvidence(t, caseFor(t, observe, Memfd))
	e.Result.Executions[0].PolicyEvents = append(e.Result.Executions[0].PolicyEvents, mountfixture.PolicyEvent{Decision: "cancelled"})
	require.NoError(t, ValidateEvidence(e), "kernel-confirmed cancellation is not another application start")
	for _, target := range []string{"/cache/image", "/deployment/app", "pipe:[123]", "socket:[123]", "anon_inode:seccomp notify"} {
		require.Error(t, validateDescriptors(map[string]string{"4": target}))
	}
	require.Error(t, validateDescriptors(map[string]string{"bad": "target"}))
	require.Error(t, validateDescriptors(nil))
	require.NoError(t, validateDescriptors(map[string]string{"4": "anon_inode:[eventpoll]", "5": "anon_inode:[eventfd]"}))
}

func TestTelemetryRejectsMalformedTypedFieldsAndWrongFailure(t *testing.T) {
	t.Parallel()
	for _, log := range []string{
		`[microfat] {"event":"dispatch","selected_size_bytes":"not a number"}`,
		`[microfat] {"event":"error","stage":false}`,
		`[microfat] {"event":"error","stage":""}`,
	} {
		e := validEvidence(t, caseFor(t, Full, Cache))
		e.Result.Executions[0].Stderr = log
		require.Error(t, ValidateEvidence(e))
	}
	e := validEvidence(t, caseFor(t, Full, Cache))
	e.Result.Executions[0].Stderr += "[microfat:hint] expected resource failure\n"
	require.NoError(t, ValidateEvidence(e))
	e.Result.Executions[0].Stderr = strings.ReplaceAll(e.Result.Executions[0].Stderr, "no space left", "unrelated error")
	require.Error(t, ValidateEvidence(e))
}

func candidateCLIEvidence(t *testing.T, mode string) Evidence {
	t.Helper()
	e := validEvidence(t, caseFor(t, distributedCLI, mode))
	e.Lineage.PayloadSHA256 = testHash
	run := &e.Result.Executions[0]
	run.Stderr = "[microfat] " + jsonString(t, format.DispatchTelemetry{Event: format.EventDispatch, SelectedVariant: "v1",
		SelectedSHA256: testHash, SelectedSizeBytes: 32, ExecMode: expectedMode(e.Case)}) + "\n"
	run.PolicyEvents = append(run.PolicyEvents,
		mountfixture.PolicyEvent{Decision: "continue", Digest: testHash, Target: "memfd:payload", Immutable: true})
	if mode == Cache {
		e.Result.CacheSnapshots[0].Entries = []mountfixture.CacheEntry{{Name: testHash, Digest: testHash, Size: 32, Mode: 0o100700, UID: 1000}}
	}
	return e
}

func TestUnmodifiedCandidateCLIRequiresInspectedImage(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{Memfd, Cache, Auto} {
		require.NoError(t, ValidateEvidence(candidateCLIEvidence(t, mode)))
	}
	for _, failure := range []string{testExit, "json", "notification", "digest", "seals", "stderr", "source-stderr"} {
		t.Run(failure, func(t *testing.T) {
			e := candidateCLIEvidence(t, Memfd)
			run := &e.Result.Executions[0]
			switch failure {
			case testExit:
				run.ExitCode = 1
			case "json":
				run.Stdout = "{"
			case "notification":
				run.PolicyEvents = nil
			case "digest":
				run.PolicyEvents[1].Digest = testWrongValue
			case "seals":
				run.PolicyEvents[1].Immutable = false
			case "stderr":
				run.Stderr += "unexpected\n"
			case "source-stderr":
				e = validEvidence(t, caseFor(t, distributedCLI, Memfd))
				e.Result.Executions[0].Stderr = "unexpected"
			}
			require.Error(t, ValidateEvidence(e))
		})
	}
}

func TestCandidateCLIIndexDefinesExpectedImage(t *testing.T) {
	t.Parallel()
	c, _ := fakeController(t, runtime.GOARCH)
	require.NoError(t, c.acquire())
	require.NoError(t, c.buildHelpers())
	lineage, err := c.pack(Configurations()[0])
	require.NoError(t, err)
	c.options.Input = Candidate
	_, err = c.cliLineage()
	require.Error(t, err, "plain or invalid CLI cannot masquerade as a distributed fat CLI")
	cli := filepath.Join(c.products, "microfat")
	require.NoError(t, os.Remove(cli))
	require.NoError(t, copyFile(lineage.Bundle, cli, privateMode, false))
	got, err := c.cliLineage()
	require.NoError(t, err)
	require.Equal(t, c.summary.Products["reporter"], got.PayloadSHA256)
	require.Equal(t, lineage.SelectedTier, got.SelectedTier)
	require.NoError(t, os.Remove(cli))
	_, err = c.cliLineage()
	require.Error(t, err)
}
