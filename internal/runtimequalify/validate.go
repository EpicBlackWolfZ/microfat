package runtimequalify

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"golang.org/x/sys/unix"
)

func expectedFailure(c Case, phase int) []string {
	switch c.Scenario {
	case createEPERM, createENOSYS:
		if c.Mode == Memfd {
			return []string{"memfd_create"}
		}
	case seal:
		if c.Mode == Memfd {
			return []string{seal}
		}
	case execFirst:
		if c.Mode != Auto {
			return []string{"execve"}
		}
	case execAll:
		return []string{"execve"}
	case fdPressure:
		return []string{"too many open files"}
	case Full:
		return []string{"no space left"}
	case readOnly:
		return []string{"read-only file system"}
	case noExec:
		if c.Mode == Cache || c.Policy != "" {
			return []string{"permission denied"}
		}
	case procMissing, procInaccessible:
		return []string{"reading secure-execution state"}
	case busy:
		if phase == 0 {
			return []string{"text file busy"}
		}
	case corruptPayload:
		return []string{"corrupt", "checksum", "decompress"}
	case corruptDictionary:
		return []string{"dictionary"}
	case symlink:
		return []string{symlink}
	case fifo:
		return []string{"not a regular file"}
	case insecure:
		return []string{"unsafe"}
	case capability:
		return []string{"capabilities"}
	}
	return nil
}

func expectedMode(c Case) string {
	if c.Mode == Cache || (c.Mode == Auto && c.Policy != "" && c.Policy != observe) {
		return Cache
	}
	return Memfd
}

func ValidateEvidence(e Evidence) error {
	r := e.Result
	if err := validateRequest(e); err != nil {
		return err
	}
	if err := validateCompletion(e); err != nil {
		return err
	}
	seen := map[int]bool{}
	phases := make([]int, e.Case.Runs)
	for _, run := range r.Executions {
		if run.PID <= 1 || seen[run.PID] || run.Phase < 0 || run.Phase >= len(phases) || run.Truncated || run.Timeout {
			return errors.New("invalid, duplicate or truncated process observation")
		}
		seen[run.PID] = true
		phases[run.Phase]++
		if err := validateExecution(e, run); err != nil {
			return err
		}
	}
	for _, count := range phases {
		if count != e.Case.Workers {
			return errors.New("incomplete batch phase")
		}
	}
	return validateCacheEvidence(e)
}

func validateCompletion(e Evidence) error {
	r := e.Result
	if r.Schema != Schema || r.Stage != "complete" || r.Error != "" || e.Stderr != "" ||
		r.Namespace == "" || r.Namespace == e.Request.ParentNamespace ||
		r.PIDNamespace == "" || r.PIDNamespace == e.Request.ParentPIDNamespace || r.UIDMap == "" || r.GIDMap == "" ||
		r.MountInfo == "" || len(r.Executions) != e.Case.Workers*e.Case.Runs {
		return errors.New("invalid fixture completion, namespace identity or process count")
	}
	if !digestPattern.MatchString(e.Lineage.BundleSHA256) || !digestPattern.MatchString(e.ExecutedSHA256) {
		return errors.New("missing executed artifact hash")
	}
	corrupted := e.Case.Scenario == corruptPayload || e.Case.Scenario == corruptDictionary
	if (e.ExecutedSHA256 != e.Lineage.BundleSHA256) != corrupted {
		return errors.New("executed artifact differs from declared fixture lineage")
	}
	return nil
}

func validateRequest(e Evidence) error {
	r := e.Request
	if r.Runtime == nil || r.Runtime.Workers != e.Case.Workers || r.Runs != e.Case.Runs ||
		r.Runtime.Policy != e.Case.Policy || r.Pause || r.Binfmt != nil || r.UID <= 0 || r.GID <= 0 {
		return errors.New("fixture request does not match required case")
	}
	cache, proc := "", "normal"
	switch e.Case.Scenario {
	case Full, noExec, readOnly:
		cache = e.Case.Scenario
	case readOnlyWarm:
		cache = readOnly
	case procNoexec, procMissing, procInaccessible:
		proc = strings.TrimPrefix(e.Case.Scenario, "proc-")
	}
	if r.Runtime.Cache != cache || r.Proc != proc || r.Runtime.Capability != (e.Case.Scenario == capability) {
		return errors.New("fixture mount or capability policy mismatch")
	}
	if (r.Runtime.BusyName == e.Lineage.PayloadSHA256) != (e.Case.Scenario == busy) && e.Lineage.PayloadSHA256 != "" {
		return errors.New("missing writable inode control")
	}
	return nil
}

func validateExecution(e Evidence, run mountfixture.Execution) error {
	for _, crash := range []string{"panic:", "fatal error:", "runtime: ", "mount-fixture child:"} {
		if strings.Contains(run.Stderr, crash) {
			return errors.New("unexpected crash or fixture failure")
		}
	}
	if e.Case.Scenario == distributedCLI {
		return validateCLI(e, run)
	}
	if e.Case.Scenario == policyControl {
		return validatePolicyControl(e.Case, run)
	}
	failure := expectedFailure(e.Case, run.Phase)
	dispatches, err := validateTelemetry(e, run, len(failure) != 0)
	if err != nil {
		return err
	}
	if err := validatePolicyEvents(e, run); err != nil {
		return err
	}
	if len(failure) > 0 {
		if run.ExitCode != 1 || run.Signal != "" || run.Started != "" || run.Stdout != "" {
			return errors.New("expected launcher refusal before application startup")
		}
		found := false
		for _, detail := range failure {
			found = found || strings.Contains(strings.ToLower(run.Stderr), detail)
		}
		if !found {
			return fmt.Errorf("missing expected failure detail %v", failure)
		}
		if strings.HasPrefix(e.Case.Scenario, "corrupt-") && len(dispatches) != 0 {
			return errors.New("corrupt payload must not reach dispatch")
		}
		return nil
	}
	if len(dispatches) == 0 || dispatches[len(dispatches)-1].ExecMode != expectedMode(e.Case) {
		return errors.New("missing or incorrect verified dispatch")
	}
	if e.Case.Scenario == "signal" {
		if run.ExitCode != -1 || run.Signal != "terminated" {
			return errors.New("payload signal was not preserved")
		}
	} else if run.ExitCode != payloadExit || run.Signal != "" {
		return errors.New("payload exit was not preserved")
	}
	if run.Started != Startup {
		return errors.New("payload must start exactly once")
	}
	return validateReport(e, run)
}

func validatePolicyControl(c Case, run mountfixture.Execution) error {
	var result mountfixture.PolicyProbe
	if run.ExitCode != 0 || run.Signal != "" || run.Started != Startup || run.Stderr != "" ||
		len(run.PolicyEvents) != 0 || json.Unmarshal([]byte(run.Stdout), &result) != nil {
		return errors.New("invalid independent policy control")
	}
	want := mountfixture.PolicyProbe{}
	switch c.Policy {
	case createEPERM:
		want.MemfdErrno = int(unix.EPERM)
	case createENOSYS:
		want.MemfdErrno = int(unix.ENOSYS)
	case seal:
		want.SealErrno = int(unix.EPERM)
	}
	if result != want {
		return errors.New("independent kernel policy result mismatch")
	}
	return nil
}
func validateCLI(e Evidence, run mountfixture.Execution) error {
	var detected struct{ Arch, Level string }
	if run.ExitCode != 0 || run.Signal != "" || run.Started != "" || json.Unmarshal([]byte(run.Stdout), &detected) != nil ||
		!slices.Contains([]string{amd64, arm64}, detected.Arch) || detected.Level == "" {
		return errors.New("distributed CLI failed")
	}
	events := run.PolicyEvents
	run.PolicyEvents = nil
	for _, event := range events {
		if event.Decision != "cancelled" {
			run.PolicyEvents = append(run.PolicyEvents, event)
		}
	}
	if e.Lineage.PayloadSHA256 == "" {
		if run.Stderr != "" || len(run.PolicyEvents) != 1 || run.PolicyEvents[0].Decision != "bootstrap" {
			return errors.New("unexpected source CLI execution")
		}
		return nil
	}
	if len(run.PolicyEvents) != 2 || run.PolicyEvents[0].Decision != "bootstrap" {
		return errors.New("candidate CLI did not perform one payload exec")
	}
	payload := run.PolicyEvents[1]
	if payload.Decision != "continue" || payload.Digest != e.Lineage.PayloadSHA256 || payload.Target == "" {
		return errors.New("candidate CLI executed wrong image")
	}
	if e.Case.Mode != Cache && !payload.Immutable {
		return errors.New("candidate CLI memfd not immutable")
	}
	_, err := validateTelemetry(e, run, false)
	return err
}

func validateReport(e Evidence, run mountfixture.Execution) error {
	var report mountfixture.Report
	if err := json.Unmarshal([]byte(run.Stdout), &report); err != nil {
		return err
	}
	if report.Digest != e.Lineage.PayloadSHA256 || report.PID != run.PID || report.Identity != "original" ||
		report.UID != e.Request.UID || report.EUID != report.UID || report.GID != e.Request.GID || report.EGID != report.GID ||
		report.Capabilities != "0000000000000000" || report.NoNewPrivileges != "1" ||
		report.Stdin != e.Request.Stdin || report.Environment != "preserved" || report.WorkingDirectory != "/" ||
		!slices.Equal(report.Args, append([]string{e.Request.Command}, e.Request.Args...)) {
		return errors.New("payload identity, credentials or process fidelity mismatch")
	}
	if len(report.Errors) != 0 || report.Original != e.Request.Command || report.Mode != expectedMode(e.Case) {
		return errors.New("payload reported an error or wrong dispatch environment")
	}
	return validateDescriptors(report.Descriptors)
}

func validateDescriptors(descriptors map[string]string) error {
	if descriptors == nil {
		return errors.New("missing payload descriptor observation")
	}
	for number, target := range descriptors {
		fd, err := strconv.Atoi(number)
		if err != nil || fd < 0 {
			return errors.New("invalid reported descriptor")
		}
		if fd > 2 && target != "anon_inode:[eventpoll]" && target != "anon_inode:[eventfd]" {
			return fmt.Errorf("launcher descriptor leaked: %s", target)
		}
	}
	return nil
}

func validateTelemetry(e Evidence, run mountfixture.Execution, failure bool) ([]format.DispatchTelemetry, error) {
	var dispatches []format.DispatchTelemetry
	var sequence []string
	for line := range strings.SplitSeq(strings.TrimSpace(run.Stderr), "\n") {
		if line == "" {
			continue
		}
		raw, ok := strings.CutPrefix(line, "[microfat] ")
		if !ok {
			if failure && strings.HasPrefix(line, "[microfat:hint] ") {
				continue
			}
			return nil, errors.New("unexpected launcher stderr")
		}
		if strings.HasPrefix(raw, "error: ") && failure {
			continue
		}
		var header struct {
			Event           string
			SelectedVariant string `json:"selected_variant"`
		}
		if err := json.Unmarshal([]byte(raw), &header); err != nil {
			return nil, err
		}
		if header.SelectedVariant != "" && header.SelectedVariant != e.Lineage.SelectedTier {
			return nil, errors.New("policy changed selected CPU tier")
		}
		switch header.Event {
		case format.EventDispatch:
			var d format.DispatchTelemetry
			if err := json.Unmarshal([]byte(raw), &d); err != nil {
				return nil, err
			}
			if d.SelectedSHA256 != e.Lineage.PayloadSHA256 || d.SelectedSizeBytes != e.Lineage.PayloadSize ||
				!slices.Contains([]string{Memfd, Cache}, d.ExecMode) {
				return nil, errors.New("wrong dispatch image")
			}
			dispatches = append(dispatches, d)
			sequence = append(sequence, "dispatch/"+d.ExecMode)
		case format.EventError:
			var d format.ErrorTelemetry
			if err := json.Unmarshal([]byte(raw), &d); err != nil {
				return nil, err
			}
			if d.Stage == "" || d.Error == "" {
				return nil, errors.New("incomplete error telemetry")
			}
			sequence = append(sequence, d.Stage)
			if d.Stage == "memfd_create" {
				want := map[string]string{createEPERM: "EPERM", createENOSYS: "ENOSYS", fdPressure: "EMFILE"}[e.Case.Policy]
				if want == "" || d.ErrnoName != want {
					return nil, errors.New("wrong kernel creation failure")
				}
			}
		default:
			return nil, errors.New("unexpected telemetry event")
		}
	}
	if !slices.Equal(sequence, expectedSequence(e.Case, run.Phase)) {
		return nil, fmt.Errorf("unexpected telemetry sequence: %v", sequence)
	}
	return dispatches, nil
}

func validatePolicyEvents(e Evidence, run mountfixture.Execution) error {
	events := make([]mountfixture.PolicyEvent, 0, len(run.PolicyEvents))
	cancelled := 0
	for _, event := range run.PolicyEvents {
		if event.Decision == "cancelled" {
			cancelled++
		} else {
			events = append(events, event)
		}
	}
	const cancellationLimit = 128
	if cancelled > cancellationLimit {
		return errors.New("too many cancelled policy notifications")
	}
	run.PolicyEvents = events
	policy := e.Case.Policy
	if policy != observe && policy != execFirst && policy != execAll && policy != fdPressure {
		if len(run.PolicyEvents) != 0 {
			return errors.New("unexpected policy events")
		}
		return nil
	}
	if policy == fdPressure {
		if len(run.PolicyEvents) != 1 || run.PolicyEvents[0].Decision != "continue-with-nofile-zero" {
			return errors.New("missing real resource-limit control")
		}
		return nil
	}
	count := 2
	if e.Case.Mode == Auto {
		count++
	}
	if len(run.PolicyEvents) != count || run.PolicyEvents[0].Decision != "bootstrap" {
		return errors.New("unexpected exec policy sequence")
	}
	for i, event := range run.PolicyEvents[1:] {
		want := "continue"
		if policy == execAll || (policy == execFirst && i == 0) {
			want = "deny-EPERM"
		}
		if event.Decision != want || event.Digest != e.Lineage.PayloadSHA256 || event.Target == "" {
			return errors.New("incorrect policy decision or inspected target")
		}
		if policy == observe && e.Case.Mode == Memfd && !event.Immutable {
			return errors.New("memfd was not proven immutable")
		}
	}
	return nil
}

func validateCacheEvidence(e Evidence) error {
	if len(e.Result.CacheSnapshots) != e.Case.Runs {
		return errors.New("incomplete cache snapshots")
	}
	for phase, snapshot := range e.Result.CacheSnapshots {
		if snapshot.Phase != phase || snapshot.Filesystem == 0 {
			return errors.New("invalid cache filesystem observation")
		}
		switch e.Request.Runtime.Cache {
		case readOnly:
			if snapshot.Flags&unix.ST_RDONLY == 0 {
				return errors.New("cache was not read-only")
			}
		case noExec:
			if snapshot.Flags&unix.ST_NOEXEC == 0 {
				return errors.New("cache was not noexec")
			}
		case Full:
			if snapshot.Filesystem != unix.TMPFS_MAGIC {
				return errors.New("exhaustion did not use private tmpfs")
			}
		}
		executedCache := (e.Case.Scenario != distributedCLI || e.Lineage.PayloadSHA256 != "") && expectedMode(e.Case) == Cache &&
			expectedFailure(e.Case, phase) == nil
		found := false
		for _, entry := range snapshot.Entries {
			if strings.HasPrefix(entry.Name, ".") && entry.Name != ".space-reservation" {
				return errors.New("staging file remained")
			}
			if entry.Name == e.Lineage.PayloadSHA256 && executedCache {
				if entry.Digest != e.Lineage.PayloadSHA256 || entry.Size != e.Lineage.PayloadSize ||
					entry.Mode != 0o100700 || int(entry.UID) != e.Request.UID {
					return errors.New("unsafe or corrupt final cache image")
				}
				found = true
			}
		}
		if executedCache && !found {
			return errors.New("missing verified cache image")
		}
	}
	return nil
}

// expectedSequence is the scenario contract, including pre-dispatch failures and
// the one permitted auto fallback. Application termination never adds an attempt.
func expectedSequence(c Case, phase int) []string {
	if c.Scenario == procMissing || c.Scenario == procInaccessible || c.Scenario == capability {
		return nil
	}
	var sequence []string
	if c.Mode != Cache {
		switch c.Policy {
		case createEPERM, createENOSYS, fdPressure:
			sequence = append(sequence, "memfd_create")
		case seal:
			sequence = append(sequence, "memfd_seal")
		case execFirst, execAll:
			sequence = append(sequence, "dispatch/memfd", "execve_memfd")
		}
		if len(sequence) > 0 && c.Mode == Memfd {
			return append(sequence, "launcher_main")
		}
	}
	if expectedFailure(c, phase) == nil {
		return append(sequence, "dispatch/"+expectedMode(c))
	}
	switch c.Scenario {
	case corruptPayload, corruptDictionary:
		if c.Mode == Cache {
			sequence = append(sequence, "cache_extract")
		} else {
			sequence = append(sequence, "extract_memfd")
		}
	case fdPressure:
		sequence = append(sequence, "cache_dir_init")
	case Full:
		sequence = append(sequence, "cache_extract")
	case readOnly, symlink, fifo, insecure:
		sequence = append(sequence, "cache_create_temp")
	case busy, noExec, execFirst, execAll:
		sequence = append(sequence, "dispatch/cache", "execve_cache")
	}
	return append(sequence, "launcher_main")
}
