package runtimequalify

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const testHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testSource = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func jsonString(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func validEvidence(t *testing.T, c Case) Evidence {
	t.Helper()
	e := Evidence{Case: c, Status: Pass, ExecutedSHA256: testHash,
		Lineage: Lineage{Bundle: "fixture", BundleSHA256: testHash, StubSHA256: testHash,
			PayloadSHA256: testHash, PayloadSize: 32, SelectedTier: "v1"},
		Request: mountfixture.Request{Root: "/fixture", UID: 1000, GID: 1000, Command: payloadPath, Args: []string{"--exit-42", "literal"},
			Stdin: "input", Runs: c.Runs, ParentNamespace: "mnt:parent", ParentPIDNamespace: "pid:parent", Proc: "normal",
			Runtime: &mountfixture.Runtime{Workers: c.Workers, Policy: c.Policy, Capability: c.Scenario == capability}},
		Result: mountfixture.Result{Schema: Schema, Stage: "complete", Namespace: "mnt:child", PIDNamespace: "pid:child",
			UIDMap: "1000 1000 1", GIDMap: "1000 1000 1", MountInfo: "fixture mount"}}
	switch c.Scenario {
	case Full, noExec, readOnly:
		e.Request.Runtime.Cache = c.Scenario
	case readOnlyWarm:
		e.Request.Runtime.Cache = readOnly
	case procNoexec, procMissing, procInaccessible:
		e.Request.Proc = strings.TrimPrefix(c.Scenario, "proc-")
	case busy:
		e.Request.Runtime.BusyName = testHash
	case corruptPayload, corruptDictionary:
		e.ExecutedSHA256 = strings.Repeat("c", 64)
	}
	for phase := range c.Runs {
		snapshot := mountfixture.CacheSnapshot{Phase: phase, Filesystem: 1}
		switch e.Request.Runtime.Cache {
		case Full:
			snapshot.Filesystem = unix.TMPFS_MAGIC
		case noExec:
			snapshot.Flags = unix.ST_NOEXEC
		case readOnly:
			snapshot.Flags = unix.ST_RDONLY
		}
		if expectedMode(c) == Cache && expectedFailure(c, phase) == nil && c.Scenario != distributedCLI {
			snapshot.Entries = []mountfixture.CacheEntry{{Name: testHash, Digest: testHash, Size: 32, Mode: 0o100700, UID: 1000}}
		}
		e.Result.CacheSnapshots = append(e.Result.CacheSnapshots, snapshot)
		for worker := range c.Workers {
			run := mountfixture.Execution{PID: 100 + phase*c.Workers + worker, Phase: phase, ExitCode: payloadExit, Started: Startup}
			report := mountfixture.Report{Identity: "original", Digest: testHash, PID: run.PID, UID: 1000, EUID: 1000, GID: 1000, EGID: 1000,
				Args: append([]string{payloadPath}, e.Request.Args...), Stdin: e.Request.Stdin, Environment: "preserved",
				WorkingDirectory: "/", Capabilities: "0000000000000000", NoNewPrivileges: "1", Original: payloadPath, Mode: expectedMode(c),
				Descriptors: map[string]string{"0": "pipe:[1]"}, Errors: map[string]string{}}
			run.Stdout = jsonString(t, report)
			for _, event := range expectedSequence(c, phase) {
				if mode, ok := strings.CutPrefix(event, "dispatch/"); ok {
					run.Stderr += "[microfat] " + jsonString(t, format.DispatchTelemetry{Event: format.EventDispatch,
						SelectedVariant: "v1", SelectedSHA256: testHash, SelectedSizeBytes: 32, ExecMode: mode}) + "\n"
				} else {
					errno := ""
					if event == "memfd_create" {
						errno = map[string]string{createEPERM: "EPERM", createENOSYS: "ENOSYS", fdPressure: "EMFILE"}[c.Policy]
					}
					run.Stderr += "[microfat] " + jsonString(t, format.ErrorTelemetry{Event: format.EventError, Stage: event,
						Error: "fixture error", ErrnoName: errno, SelectedVariant: "v1"}) + "\n"
				}
			}
			if failures := expectedFailure(c, phase); failures != nil {
				run.ExitCode, run.Started, run.Stdout = 1, "", ""
				run.Stderr += "[microfat] error: " + strings.Join(failures, ", ") + "\n"
			}
			if c.Scenario == "signal" {
				run.ExitCode, run.Signal = -1, "terminated"
			}
			switch c.Policy {
			case observe, execFirst, execAll:
				run.PolicyEvents = []mountfixture.PolicyEvent{{Decision: "bootstrap"}}
				count := 1
				if c.Mode == Auto {
					count++
				}
				for n := range count {
					decision := "continue"
					if c.Policy == execAll || (c.Policy == execFirst && n == 0) {
						decision = "deny-EPERM"
					}
					run.PolicyEvents = append(run.PolicyEvents, mountfixture.PolicyEvent{Decision: decision, Target: "/fixture",
						Digest: testHash, Immutable: c.Policy == observe})
				}
			case fdPressure:
				run.PolicyEvents = []mountfixture.PolicyEvent{{Decision: "continue-with-nofile-zero"}}
			}
			if c.Scenario == distributedCLI {
				run.ExitCode, run.Started, run.Stdout, run.Stderr = 0, "", `{"Arch":"amd64","Level":"v1"}`, ""
				run.PolicyEvents = []mountfixture.PolicyEvent{{Decision: "bootstrap"}}
			}
			if c.Scenario == policyControl {
				report := mountfixture.PolicyProbe{}
				switch c.Policy {
				case createEPERM:
					report.MemfdErrno = int(unix.EPERM)
				case createENOSYS:
					report.MemfdErrno = int(unix.ENOSYS)
				case seal:
					report.SealErrno = int(unix.EPERM)
				}
				run.ExitCode, run.Stdout, run.Stderr = 0, jsonString(t, report), ""
			}
			e.Result.Executions = append(e.Result.Executions, run)
		}
	}
	if c.Scenario == distributedCLI {
		e.Lineage.PayloadSHA256 = ""
	}
	return e
}

func validSummary(t *testing.T) Summary {
	t.Helper()
	s := Summary{Schema: Schema, Status: Pass, Input: Source, Architecture: amd64, Source: testSource, Kernel: "Linux x86_64",
		PageSize: 4096, Toolchain: "go version go1.27.1 linux/amd64", Expected: Cases(),
		Products: map[string]string{"microfat": testHash, fullStub: testHash, minimalStub: testHash,
			"reporter": testHash, "mount-runner": testHash}}
	for _, c := range s.Expected {
		s.Results = append(s.Results, validEvidence(t, c))
	}
	return s
}

func TestCompleteMatrixAndEvidence(t *testing.T) {
	t.Parallel()
	require.Len(t, Configurations(), 16)
	seen := map[string]bool{}
	for _, c := range Cases() {
		require.False(t, seen[c.ID], "duplicate case %s", c.ID)
		seen[c.ID] = true
		t.Run(c.ID, func(t *testing.T) { require.NoError(t, ValidateEvidence(validEvidence(t, c))) })
	}
	require.NoError(t, ValidateSummary(validSummary(t)))
}

func TestEvidenceRejectsInvalidObservations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*Evidence)
	}{
		{"schema", func(e *Evidence) { e.Result.Schema++ }},
		{"setup-failure", func(e *Evidence) { e.Result.Stage = "setup" }},
		{"error", func(e *Evidence) { e.Result.Error = "failure" }},
		{"stderr", func(e *Evidence) { e.Stderr = "unexpected" }},
		{"parent-mount", func(e *Evidence) { e.Result.Namespace = e.Request.ParentNamespace }},
		{"parent-pid", func(e *Evidence) { e.Result.PIDNamespace = e.Request.ParentPIDNamespace }},
		{"mapping", func(e *Evidence) { e.Result.UIDMap = "" }},
		{"mountinfo", func(e *Evidence) { e.Result.MountInfo = "" }},
		{"missing-worker", func(e *Evidence) { e.Result.Executions = e.Result.Executions[1:] }},
		{"duplicate-worker", func(e *Evidence) { e.Result.Executions[1].PID = e.Result.Executions[0].PID }},
		{"bad-pid", func(e *Evidence) { e.Result.Executions[0].PID = 1 }},
		{"bad-phase", func(e *Evidence) { e.Result.Executions[0].Phase = -1 }},
		{"wrong-phase-count", func(e *Evidence) { e.Result.Executions[0].Phase = 1 }},
		{"truncated", func(e *Evidence) { e.Result.Executions[0].Truncated = true }},
		{"missing-hash", func(e *Evidence) { e.Lineage.BundleSHA256 = "" }},
		{"wrong-payload", func(e *Evidence) { e.Lineage.PayloadSHA256 = strings.Repeat("b", 64) }},
		{"wrong-size", func(e *Evidence) { e.Lineage.PayloadSize++ }},
		{"wrong-tier", func(e *Evidence) { e.Lineage.SelectedTier = "v2" }},
		{"crash", func(e *Evidence) { e.Result.Executions[0].Stderr += "panic: injected\n" }},
		{"extra-log", func(e *Evidence) { e.Result.Executions[0].Stderr += "unknown log\n" }},
		{"malformed-log", func(e *Evidence) { e.Result.Executions[0].Stderr = "[microfat] bad json\n" }},
		{"unknown-event", func(e *Evidence) { e.Result.Executions[0].Stderr = `[microfat] {"event":"unknown"}` }},
		{"no-dispatch", func(e *Evidence) { e.Result.Executions[0].Stderr = "" }},
		{"duplicate-dispatch", func(e *Evidence) { e.Result.Executions[0].Stderr += e.Result.Executions[0].Stderr }},
		{"wrong-exit", func(e *Evidence) { e.Result.Executions[0].ExitCode = 0 }},
		{"wrong-signal", func(e *Evidence) { e.Result.Executions[0].Signal = "killed" }},
		{"missing-start", func(e *Evidence) { e.Result.Executions[0].Started = "" }},
		{"double-start", func(e *Evidence) { e.Result.Executions[0].Started += Startup }},
		{"bad-report", func(e *Evidence) { e.Result.Executions[0].Stdout = "not json" }},
		{"report-error", func(e *Evidence) {
			var report mountfixture.Report
			require.NoError(t, json.Unmarshal([]byte(e.Result.Executions[0].Stdout), &report))
			report.Errors["digest"] = "failure"
			e.Result.Executions[0].Stdout = jsonString(t, report)
		}},
		{"wrong-args", func(e *Evidence) { e.Request.Args = []string{"changed"} }},
		{"wrong-uid", func(e *Evidence) { e.Request.UID++ }},
		{"missing-cache", func(e *Evidence) { e.Result.CacheSnapshots = nil }},
		{"wrong-filesystem", func(e *Evidence) { e.Result.CacheSnapshots[0].Filesystem = 0 }},
		{"wrong-cache-phase", func(e *Evidence) { e.Result.CacheSnapshots[0].Phase++ }},
		{"wrong-cache-mode", func(e *Evidence) { e.Result.CacheSnapshots[0].Entries[0].Mode = 0o100777 }},
		{"wrong-cache-hash", func(e *Evidence) { e.Result.CacheSnapshots[0].Entries[0].Digest = testWrongValue }},
		{"missing-cache-file", func(e *Evidence) { e.Result.CacheSnapshots[0].Entries = nil }},
		{"staging-file", func(e *Evidence) { e.Result.CacheSnapshots[0].Entries[0].Name = ".staging" }},
		{"unexpected-policy", func(e *Evidence) {
			e.Result.Executions[0].PolicyEvents = []mountfixture.PolicyEvent{{Decision: "deny"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := validEvidence(t, Cases()[4])
			tc.mutate(&e)
			require.Error(t, ValidateEvidence(e))
		})
	}
}

func TestSummaryRequiresEntireMatrix(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"schema", testStatus, "input", "arch", "source", "kernel", "pages", "toolchain", "missing", "expected",
		"failed", "duplicate", "unexpected", "reason", "invalid-observation"} {
		t.Run(name, func(t *testing.T) {
			s := validSummary(t)
			switch name {
			case "schema":
				s.Schema++
			case testStatus:
				s.Status = Incomplete
			case "input":
				s.Input = "qemu"
			case "arch":
				s.Architecture = "386"
			case "source":
				s.Source = ""
			case "kernel":
				s.Kernel = ""
			case "pages":
				s.PageSize = 0
			case "toolchain":
				s.Toolchain = "go1.26"
			case "missing":
				s.Results = s.Results[1:]
			case "expected":
				s.Expected = slices.Clone(s.Expected)
				s.Expected[0].Workers++
			case "failed":
				s.Results[0].Status = Fail
			case "duplicate":
				s.Results[1] = s.Results[0]
			case "unexpected":
				s.Results[0].Case.ID = "unknown"
			case "reason":
				s.Results[0].Reason = "skipped"
			case "invalid-observation":
				s.Results[0].Result.Executions = nil
			}
			require.Error(t, ValidateSummary(s))
		})
	}
}

func TestCandidateProvenance(t *testing.T) {
	t.Parallel()
	base := Summary{Input: Candidate, Tag: testCandidateTag, ChecksumsSHA256: testHash, RunID: "123", Attempt: "2",
		Assets: map[string]string{}, Release: Release{ID: 1, Tag: testCandidateTag, Draft: true,
			Assets: []Asset{{ID: 1, Name: "checksums.txt", Size: 1, Digest: "sha256:" + testHash},
				{ID: 2, Name: "checksums.txt.sig", Size: 1, Digest: "sha256:" + testHash}}}}
	contract, err := releasecheck.NewReleaseContract(base.Tag)
	require.NoError(t, err)
	for name := range contract.ExpectedPayloadNames {
		base.Assets[name] = testHash
		base.Release.Assets = append(base.Release.Assets,
			Asset{ID: int64(len(base.Release.Assets) + 1), Name: name, Size: 1, Digest: "sha256:" + testHash})
	}
	require.NoError(t, ValidateCandidate(base))
	for _, name := range []string{"dirty", "tag", "id", "release-tag", "published", "checksum", "inventory",
		"asset-id", "asset-size", "duplicate", "asset-digest", "checksum-digest", "missing-product", "missing-signature"} {
		t.Run(name, func(t *testing.T) {
			s := base
			s.Release.Assets = slices.Clone(base.Release.Assets)
			switch name {
			case "dirty":
				s.Dirty = true
			case "tag":
				s.Tag = "latest"
			case "id":
				s.Release.ID = 0
			case "release-tag":
				s.Release.Tag = "v0.2.5"
			case "published":
				s.Release.Draft = false
			case "checksum":
				s.ChecksumsSHA256 = ""
			case "inventory":
				s.Assets = nil
			case "asset-id":
				s.Release.Assets[0].ID = 0
			case "asset-size":
				s.Release.Assets[0].Size = 0
			case "duplicate":
				s.Release.Assets = append(s.Release.Assets, s.Release.Assets[0])
			case "asset-digest":
				s.Release.Assets[0].Digest = testWrongValue
			case "checksum-digest":
				s.Release.Assets[0].Digest = testWrongValue
			case "missing-product":
				s.Release.Assets = s.Release.Assets[1:]
			case "missing-signature":
				s.Release.Assets = append(s.Release.Assets[:1], s.Release.Assets[2:]...)
			}
			require.Error(t, ValidateCandidate(s), fmt.Sprint(s))
		})
	}
}
