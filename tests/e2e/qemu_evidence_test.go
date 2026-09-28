//go:build linux

package e2e_test

import (
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
)

const (
	qemuAuto        = "auto"
	qemuUserNS      = "userns"
	qemuStripped    = "-ldflags=-s -w"
	qemuDuplicate   = "duplicate"
	qemuUnknown     = "unknown"
	qemuModeEnv     = "MICROFAT_QEMU_TESTS"
	qemuBackendEnv  = "MICROFAT_QEMU_BACKEND"
	qemuOutputEnv   = "MICROFAT_QEMU_OUTPUT"
	qemuEmulatorEnv = "MICROFAT_QEMU_AARCH64"
	qemuLimitation  = "expected-descriptor-limitation"
	qemuSuccess     = "payload-started"
	qemuOptimize    = "verified-extraction"
	qemuMinimal     = "minimal-meta-rejection"
	qemuDisk        = "disk"
	qemuKeep        = "keep"
	qemuCloexec     = "cloexec"
	qemuHandler     = "microfat-qemu-arm64"
	qemuProbePrefix = "[qemu-probe] "
	qemuFailureExit = 1
)

type qemuDispatch struct {
	Event  string `json:"event"`
	Digest string `json:"selected_sha256"`
	Size   int64  `json:"selected_size_bytes"`
	Mode   string `json:"exec_mode"`
}

type qemuEvidence struct {
	mountEvidence
	Timeout           time.Duration                 `json:"timeout_ns"`
	ControllerTimeout time.Duration                 `json:"controller_timeout_ns"`
	Outcome           string                        `json:"outcome"`
	Probe             *mountfixture.DescriptorProbe `json:"probe,omitempty"`
	Dispatch          *qemuDispatch                 `json:"dispatch,omitempty"`
	CacheBefore       map[string]string             `json:"cache_before"`
	CacheAfter        map[string]string             `json:"cache_after"`
}

type qemuSummary struct {
	mountSummary
	Target          string            `json:"target"`
	Emulator        string            `json:"emulator"`
	EmulatorVersion string            `json:"emulator_version"`
	EmulatorSHA256  string            `json:"emulator_sha256"`
	Provenance      string            `json:"provenance"`
	ParentBinfmt    map[string]string `json:"parent_binfmt"`
	Outcomes        map[string]string `json:"outcomes"`
}

func qemuExpected() []string {
	ids := []string{"controls/explicit-qemu", "controls/binfmt-raw"}
	for _, storage := range []string{qemuDisk, execModeMemfd} {
		for _, policy := range []string{qemuCloexec, qemuKeep} {
			ids = append(ids, "controls/arm64/"+storage+"/"+policy)
		}
		ids = append(ids, "controls/native/"+storage+"/"+qemuCloexec)
	}
	for _, c := range mountConfigurations() {
		for _, mode := range []string{execModeMemfd, "cache-cold", "cache-warm", qemuAuto} {
			ids = append(ids, c.name()+"/"+mode)
		}
		ids = append(ids, c.name()+"/lifecycle")
	}
	return ids
}

func validateQemuSummary(summary qemuSummary) error {
	if !slices.Equal(summary.Expected, qemuExpected()) {
		return errors.New("QEMU expected-case manifest does not match the qualification matrix")
	}
	if err := validateMountSummary(summary.mountSummary); err != nil {
		return err
	}
	if len(summary.Outcomes) != len(summary.Expected) {
		return errors.New("incomplete QEMU outcomes")
	}
	for _, id := range summary.Expected {
		if summary.Outcomes[id] != qemuExpectedOutcome(id) {
			return fmt.Errorf("invalid outcome for %s", id)
		}
	}
	return nil
}

func qemuExpectedOutcome(id string) string {
	if strings.HasPrefix(id, "controls/") {
		if strings.HasPrefix(id, "controls/arm64/") && strings.HasSuffix(id, "/cloexec") {
			return qemuLimitation
		}
		return qemuSuccess
	}
	if strings.HasSuffix(id, "/lifecycle") {
		if strings.Contains(id, "-minimal-") {
			return qemuMinimal
		}
		return qemuOptimize
	}
	return qemuLimitation
}

func validateQemuEmulator(path string) error {
	file, err := elf.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if file.Machine != elf.EM_X86_64 || file.Class != elf.ELFCLASS64 {
		return errors.New("emulator must be a native amd64 ELF")
	}
	for _, program := range file.Progs {
		if program.Type == elf.PT_INTERP {
			return errors.New("emulator must be static")
		}
	}
	return nil
}

func validateQemuRegistration(raw, interpreter string) error {
	want := "enabled\ninterpreter " + interpreter + "\nflags: F\noffset 0\n" +
		"magic 7f454c460201010000000000000000000200b700\n" +
		"mask ffffffffffffff00fffffffffffffffffeffffff\n"
	if raw != want {
		return fmt.Errorf("unexpected binfmt registration: %q", raw)
	}
	return nil
}

func qemuParentBinfmt(t *testing.T) map[string]string {
	t.Helper()
	const dir = "/proc/sys/fs/binfmt_misc"
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{"availability": "not mounted"}
	}
	require.NoError(t, err)
	result := map[string]string{}
	for _, entry := range entries {
		if entry.Name() == "register" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)
		result[entry.Name()] = string(data)
	}
	return result
}

func qemuLimitationResult(run mountfixture.Execution, digest, mode string, size int64) (*qemuDispatch, error) {
	if err := validateQemuFailure(run); err != nil {
		return nil, err
	}
	var dispatch qemuDispatch
	if err := decodeQemuTelemetry(run.Stderr, "[microfat] ", &dispatch); err != nil {
		return nil, err
	}
	if dispatch.Event != "dispatch" || dispatch.Digest != digest || dispatch.Size != size || dispatch.Mode != mode {
		return nil, errors.New("missing or incorrect verified dispatch telemetry")
	}
	return &dispatch, nil
}

func validateQemuFailure(run mountfixture.Execution) error {
	if run.ExitCode != qemuFailureExit || run.Started != "" || run.Stdout != "" {
		return errors.New("expected QEMU exit 1 without payload startup or stdout")
	}
	return nil
}

func decodeQemuTelemetry(stderr, prefix string, record any) error {
	// The qualified interpreter failure is silent. Only the single pre-exec
	// record is allowed; a panic or other diagnostic must never count as it.
	data, ok := strings.CutPrefix(strings.TrimSuffix(stderr, "\n"), prefix)
	if !ok || strings.ContainsRune(data, '\n') {
		return errors.New("expected exactly one telemetry record without additional diagnostics")
	}
	return json.Unmarshal([]byte(data), record)
}

func qemuProbeRecord(stderr string) (mountfixture.DescriptorProbe, error) {
	var result mountfixture.DescriptorProbe
	if err := decodeQemuTelemetry(stderr, qemuProbePrefix, &result); err != nil {
		return result, err
	}
	if result.Event != "before-exec" || result.Error != "" {
		return result, errors.New("probe did not reach exec exactly once or exec returned")
	}
	return result, nil
}
