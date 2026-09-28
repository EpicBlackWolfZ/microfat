//go:build linux

package e2e_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
)

const qemuControllerTimeout = 45 * time.Second

var qemuSupervisorOnce sync.Once
var qemuSupervisorError error

func qemuSupervisorPath() string { return filepath.Join(e2eRootDir, "userns-runner") }

func buildQemuSupervisor() error {
	qemuSupervisorOnce.Do(func() {
		qemuSupervisorError = compileBinary("./testdata/userns_runner", qemuSupervisorPath(), []string{envStatic})
	})
	return qemuSupervisorError
}

type qemuProducts struct {
	mountProducts
	probe, nativeProbe, nativeReporter, emulator string
}

type qemuHarness struct {
	backend, output string
	products        qemuProducts
	summary         qemuSummary
}

func qemuNamespaceCommand(backend string, command ...string) []string {
	if backend != mountSudo {
		return mountNamespaceCommand(qemuUserNS, command...)
	}
	// Clear supplementary groups before setgroups is disabled by userns mapping.
	// Keep root for setup plus exactly the ordinary payload UID/GID.
	args := []string{mountSudo, "-n", "--", "setpriv", "--clear-groups", qemuSupervisorPath(),
		strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid()), mountUnshare,
		"--mount", "--pid", "--fork", "--kill-child=SIGKILL", "--mount-proc", "--propagation", "private", "--"}
	return append(args, command...)
}

func qemuPrerequisite(backend string) (string, error) {
	if runtime.GOARCH != archAMD64 || os.Getuid() == 0 || os.Getgid() == 0 {
		return "qualification requires an ordinary user on native Linux amd64", nil
	}
	if backend == mountSudo {
		if err := buildQemuSupervisor(); err != nil {
			return "", err
		}
	}
	args := qemuNamespaceCommand(backend, "true")
	ctx, cancel := context.WithTimeout(context.Background(), mountTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return classifyQemuPrerequisite(string(out), err)
}

func classifyQemuPrerequisite(output string, err error) (string, error) {
	if err == nil {
		return "", nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) ||
		strings.Contains(output, "Operation not permitted") || strings.Contains(output, "Permission denied") ||
		strings.Contains(output, "a password is required") || strings.Contains(output, "a terminal is required") {
		return fmt.Sprintf("namespace prerequisite unavailable: %v: %s", err, output), nil
	}
	return "", fmt.Errorf("namespace preflight failed: %w: %s", err, output)
}

func buildQemuProducts(t *testing.T, output, emulator string) qemuProducts {
	t.Helper()
	native := buildMountProducts(t, filepath.Join(output, "native"))
	require.NoError(t, buildQemuSupervisor())
	mountCopy(t, output, "native/userns-runner", qemuSupervisorPath())
	dir := filepath.Join(output, "products")
	require.NoError(t, os.MkdirAll(dir, privateDirPerm))
	p := qemuProducts{mountProducts: mountProducts{controller: native.controller, cli: native.cli,
		reporter: filepath.Join(dir, "reporter"), replacement: native.replacement,
		full: filepath.Join(dir, "microfat-stub"), minimal: filepath.Join(dir, "microfat-stub-minimal")},
		probe: filepath.Join(dir, "descriptor-probe"), nativeProbe: filepath.Join(output, "native", "descriptor-probe"),
		nativeReporter: native.reporter, emulator: filepath.Join(dir, "qemu-aarch64-static")}
	mountCopy(t, output, "products/qemu-aarch64-static", emulator)
	for _, item := range []struct{ pkg, out, flags string }{
		{"./testdata/mount_reporter", p.reporter, qemuStripped},
		{"./testdata/descriptor_probe", p.probe, qemuStripped},
		{stubPackagePath, p.full, qemuStripped},
		{stubPackagePath, p.minimal, "-tags=minimal"},
	} {
		require.NoError(t, compileBinaryWithFlags(item.pkg, item.out,
			[]string{envStatic, "GOOS=linux", "GOARCH=arm64", stubEnvARM64}, item.flags))
	}
	require.NoError(t, compileBinary("./testdata/descriptor_probe", p.nativeProbe,
		[]string{envStatic, "GOARCH=amd64", envBaselineAMD64}))
	p.digest = mountDigest(t, p.reporter)
	return p
}

func prepareQemuRequest(t *testing.T, p qemuProducts, binary, mode string) mountfixture.Request {
	t.Helper()
	req := prepareMountRequest(t, p.mountProducts, binary, "directory", mode)
	req.Runs = 1
	ns, err := os.Readlink("/proc/self/ns/user")
	require.NoError(t, err)
	req.Binfmt = &mountfixture.Binfmt{ParentUserNamespace: ns}
	req.Env = append(req.Env, "MICROFAT_LOG=json")
	mountCopy(t, req.Root, "rootfs/emulator", p.emulator)
	mountCopy(t, req.Root, "source/probe", p.probe)
	mountCopy(t, req.Root, "source/native-probe", p.nativeProbe)
	mountCopy(t, req.Root, "source/native-reporter", p.nativeReporter)
	mountCopy(t, req.Root, "source/raw", p.reporter)
	return req
}

func (h *qemuHarness) save(t *testing.T) {
	t.Helper()
	writeMountJSON(t, filepath.Join(h.output, "summary.json"), h.summary)
}

func (h *qemuHarness) skip(t *testing.T, mode, reason string) {
	t.Helper()
	for _, id := range h.summary.Expected {
		h.summary.Results = append(h.summary.Results, mountEvidence{Schema: 1, Case: id, Status: mountSkip, Reason: reason})
	}
	h.save(t)
	if mode == mountRequired {
		t.Fatal(reason)
	}
	t.Skip("incomplete qualification: " + reason)
}

func TestQemuQualification(t *testing.T) {
	mode := os.Getenv(qemuModeEnv)
	if mode == "" {
		t.Skip("QEMU qualification is opt-in; run task qualify-qemu")
	}
	require.Contains(t, []string{qemuAuto, mountRequired}, mode)
	backend := os.Getenv(qemuBackendEnv)
	if backend == "" {
		backend = qemuUserNS
	}
	require.Contains(t, []string{qemuUserNS, mountSudo}, backend)
	output, err := filepath.Abs(os.Getenv(qemuOutputEnv))
	require.NoError(t, err)
	require.NotEmpty(t, os.Getenv(qemuOutputEnv), "use task qualify-qemu to create an evidence directory")
	h := qemuHarness{backend: backend, output: output, summary: qemuSummary{
		mountSummary: mountSummary{Schema: 1, Status: "incomplete", Backend: backend, Architecture: runtime.GOARCH,
			Source: mountCommandOutput("git", "rev-parse", "HEAD"), Dirty: mountCommandOutput("git", "status", "--porcelain") != "",
			Kernel: mountCommandOutput("uname", "-srmo"), GoVersion: runtime.Version(), PageSize: os.Getpagesize(),
			UnshareVersion: mountCommandOutput(mountUnshare, "--version"), Expected: qemuExpected(), Products: map[string]string{}},
		Target: archARM64, Outcomes: map[string]string{}, ParentBinfmt: qemuParentBinfmt(t)}}
	h.save(t)
	defer func() {
		require.Equal(t, h.summary.ParentBinfmt, qemuParentBinfmt(t), "parent binfmt registrations changed")
		if validateQemuSummary(h.summary) == nil && !t.Failed() {
			h.summary.Status = mountPass
		}
		h.save(t)
	}()
	reason, err := qemuPrerequisite(backend)
	require.NoError(t, err)
	if reason != "" {
		h.skip(t, mode, reason)
	}
	emulator := os.Getenv(qemuEmulatorEnv)
	if emulator == "" {
		emulator, err = exec.LookPath("qemu-aarch64-static")
		if err != nil {
			h.skip(t, mode, "qemu-aarch64-static is unavailable")
		}
	}
	emulator, err = filepath.Abs(emulator)
	require.NoError(t, err)
	require.NoError(t, validateQemuEmulator(emulator))
	h.summary.Emulator, h.summary.EmulatorSHA256 = emulator, mountDigest(t, emulator)
	h.summary.EmulatorVersion = mountCommandOutput(emulator, "--version")
	require.True(t, strings.HasPrefix(h.summary.EmulatorVersion, "qemu-aarch64 version"), h.summary.EmulatorVersion)
	h.summary.Provenance = "caller-supplied executable; digest identifies exact bytes"
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(emulator), "provenance.txt")); err == nil {
		h.summary.Provenance = string(data)
	}
	h.save(t)
	h.products = buildQemuProducts(t, output, emulator)
	h.summary.Products["native/userns-runner"] = mountDigest(t, qemuSupervisorPath())
	require.Equal(t, h.summary.EmulatorSHA256, mountDigest(t, h.products.emulator), "emulator changed while staging")
	for _, path := range []string{h.products.controller, h.products.cli, h.products.reporter, h.products.full, h.products.minimal,
		h.products.probe, h.products.nativeProbe, h.products.nativeReporter, h.products.emulator} {
		relative, err := filepath.Rel(output, path)
		require.NoError(t, err)
		h.summary.Products[relative] = mountDigest(t, path)
	}
	req := prepareQemuRequest(t, h.products, h.products.reporter, execModeNative)
	req.Binfmt.Preflight = true
	request := filepath.Join(req.Root, "request.json")
	writeMountJSON(t, request, req)
	_, result, stderr, err := invokeMountArgs(qemuNamespaceCommand(backend, h.products.controller, request), qemuControllerTimeout)
	writeMountJSON(t, filepath.Join(output, "preflight.json"), result)
	if result.Prerequisite != "" {
		h.skip(t, mode, result.Prerequisite+": "+result.Error)
	}
	require.NoError(t, err, "%s: %s", stderr, result.Error)
	require.NoError(t, validateQemuRegistration(result.Registration, filepath.Join(req.Root, "rootfs/emulator")))
	require.True(t, t.Run("controls", h.controls), "independent controls must pass before classifying launcher failures")
	t.Run("namespace-guard", func(t *testing.T) {
		req := prepareQemuRequest(t, h.products, h.products.reporter, execModeNative)
		req.Binfmt.ParentUserNamespace = "USER_NAMESPACE_TOKEN"
		request, substituted := filepath.Join(req.Root, "request.json"), filepath.Join(req.Root, "child-request.json")
		writeMountJSON(t, request, req)
		const script = `sed "s|USER_NAMESPACE_TOKEN|$(readlink /proc/self/ns/user)|" "$1" > "$2"; exec "$3" "$2"`
		args := qemuNamespaceCommand(backend, "sh", "-c", script, "sh", request, substituted, h.products.controller)
		_, result, _, err := invokeMountArgs(args, qemuControllerTimeout)
		require.Error(t, err)
		require.Equal(t, "namespace", result.Stage)
		require.Contains(t, result.Error, "parent user namespace")
		require.Empty(t, result.Registration)
	})
	for _, mode := range []string{"--hang", "--spawn-detached"} {
		t.Run("timeout-reaps/"+mode, func(t *testing.T) {
			req := prepareQemuRequest(t, h.products, h.products.reporter, execModeNative)
			req.Args = []string{mode, "microfat-fixture-" + rand.Text()}
			assertFixtureTimeoutCleanup(t, req,
				qemuNamespaceCommand(backend, h.products.controller, filepath.Join(req.Root, "request.json")))
		})
	}
	for _, c := range mountConfigurations() {
		c.Binary = packMountProductArch(t, h.products.mountProducts, output, c, archARM64)
		c.Digest = mountDigest(t, c.Binary)
		h.summary.Products[filepath.Base(c.Binary)] = c.Digest
		t.Run(c.name(), func(t *testing.T) { h.matrix(t, c) })
	}
	require.NoError(t, validateQemuSummary(h.summary))
}

func (h *qemuHarness) run(t *testing.T, entry *qemuEvidence, check func(mountfixture.Execution)) {
	t.Helper()
	entry.Schema, entry.Status = 1, mountFail
	entry.Timeout, entry.ControllerTimeout = mountTimeout, qemuControllerTimeout
	entry.CacheBefore = qemuCacheSnapshot(t, entry.Request.Root)
	defer func() {
		if !t.Failed() {
			entry.Status = mountPass
		} else {
			entry.Reason = "qualification assertion failed; see tests.log"
		}
		name := strings.ReplaceAll(entry.Case, "/", "__")
		writeMountJSON(t, filepath.Join(h.output, name+".json"), entry)
		h.summary.Results = append(h.summary.Results, entry.mountEvidence)
		h.summary.Outcomes[entry.Case] = entry.Outcome
		h.save(t)
	}()
	request := filepath.Join(entry.Request.Root, "request.json")
	writeMountJSON(t, request, entry.Request)
	start := time.Now()
	var err error
	entry.Command, entry.Result, entry.HelperStderr, err = invokeMountArgs(
		qemuNamespaceCommand(h.backend, h.products.controller, request), qemuControllerTimeout)
	entry.Duration = time.Since(start)
	entry.CacheAfter = qemuCacheSnapshot(t, entry.Request.Root)
	require.NoError(t, err, "stage=%s error=%s stderr=%s", entry.Result.Stage, entry.Result.Error, entry.HelperStderr)
	require.Equal(t, "complete", entry.Result.Stage)
	require.Empty(t, entry.Result.Error)
	require.NotEqual(t, entry.Request.ParentNamespace, entry.Result.Namespace)
	require.NotEqual(t, entry.Request.ParentPIDNamespace, entry.Result.PIDNamespace)
	require.NotEqual(t, entry.Request.Binfmt.ParentUserNamespace, entry.Result.UserNamespace)
	require.NoError(t, validateQemuRegistration(entry.Result.Registration, filepath.Join(entry.Request.Root, "rootfs/emulator")))
	require.Len(t, entry.Result.Executions, 1)
	assertMountFlags(t, entry.Request, entry.Result.MountInfo)
	check(entry.Result.Executions[0])
}

func qemuEntry(id, expected, digest string, req mountfixture.Request) qemuEvidence {
	return qemuEvidence{mountEvidence: mountEvidence{Case: id, Expected: expected, PayloadSHA256: digest, Request: req},
		Outcome: expected}
}

func assertQemuPayload(t *testing.T, req mountfixture.Request, run mountfixture.Execution, digest, argv0 string) mountfixture.Report {
	t.Helper()
	require.Equal(t, expectedExitCode42, run.ExitCode, run.Stderr)
	require.Equal(t, mountStartup, run.Started)
	report, err := decodeMountReport(run.Stdout, digest)
	require.NoError(t, err, run.Stdout)
	require.Equal(t, "original", report.Identity)
	require.Equal(t, run.PID, report.PID)
	require.Equal(t, req.UID, report.UID)
	require.Equal(t, req.UID, report.EUID)
	require.Equal(t, req.GID, report.GID)
	require.Equal(t, req.GID, report.EGID)
	require.Equal(t, "/", report.WorkingDirectory)
	require.Equal(t, "0000000000000000", report.Capabilities)
	require.Equal(t, "1", report.NoNewPrivileges)
	require.Equal(t, mountStdin, report.Stdin)
	require.Equal(t, "preserved", report.Environment)
	require.Equal(t, []string{argv0, "--exit-42", mountPayloadArgs, "literal\targument"}, report.Args)
	return report
}

func qemuCacheSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	path := filepath.Join(root, execModeCache)
	result := cacheTreeSnapshot(t, path)
	delete(result, path)
	return result
}
