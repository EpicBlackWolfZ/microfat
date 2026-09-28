//go:build linux

package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
)

const (
	mountProcMissing       = "proc-missing"
	mountProcInaccessible  = "proc-inaccessible"
	mountUnlink            = "unlink"
	mountArchFlag          = "--arch"
	mountCodecZstd         = "zstd"
	mountRemovedGeneration = "generation-removed"
	mountBrokenLink        = "broken-link"
	mountSudo              = "sudo"
	mountUnshare           = "unshare"
	mountReplace           = "replace"
	mountTimeout           = 30 * time.Second
	mountModeEnv           = "MICROFAT_MOUNT_TESTS"
	mountBackendEnv        = "MICROFAT_MOUNT_BACKEND"
	mountOutputEnv         = "MICROFAT_MOUNT_OUTPUT"
	mountPayloadPath       = "/deployment space/app "
	mountPayloadArgs       = "argument with spaces"
	mountStdin             = "mount qualification stdin\n"
	mountAsset             = "original neighboring asset"
	mountExplicitAsset     = "explicit stable asset"
	mountStartup           = "payload-started\n"
	mountPass              = "pass"
	mountFail              = "fail"
	mountSkip              = "skip"
	mountRequired          = "required"
)

type mountConfiguration struct {
	Version    int    `json:"format"`
	Profile    string `json:"profile"`
	Codec      string `json:"codec"`
	Dictionary bool   `json:"dictionary"`
	Binary     string `json:"binary"`
	Digest     string `json:"sha256"`
}

func (c mountConfiguration) name() string {
	return fmt.Sprintf("v%d-%s-%s-dict%t", c.Version, c.Profile, c.Codec, c.Dictionary)
}

type mountEvidence struct {
	Schema        int                  `json:"schema"`
	Case          string               `json:"case"`
	Status        string               `json:"status"`
	Reason        string               `json:"reason,omitempty"`
	Expected      string               `json:"expected"`
	Configuration mountConfiguration   `json:"configuration"`
	PayloadSHA256 string               `json:"payload_sha256"`
	Command       []string             `json:"command"`
	Request       mountfixture.Request `json:"request"`
	Result        mountfixture.Result  `json:"result"`
	HelperStderr  string               `json:"helper_stderr,omitempty"`
	Duration      time.Duration        `json:"duration_ns"`
}

type mountSummary struct {
	Schema         int               `json:"schema"`
	Status         string            `json:"status"`
	Backend        string            `json:"backend"`
	Architecture   string            `json:"architecture"`
	Source         string            `json:"source"`
	Dirty          bool              `json:"dirty"`
	Kernel         string            `json:"kernel"`
	GoVersion      string            `json:"go_version"`
	UnshareVersion string            `json:"unshare_version"`
	PageSize       int               `json:"page_size"`
	Products       map[string]string `json:"products_sha256"`
	Expected       []string          `json:"expected"`
	Results        []mountEvidence   `json:"results"`
}

type mountProducts struct {
	controller, reporter, replacement, cli, full, minimal, digest string
}

var mountScenarios = []string{
	"directory", "symlink", "path", "file-bind", "readonly-deployment", "readonly-root", "readonly-no-cache",
	"proc-noexec", mountProcMissing, mountProcInaccessible, "mount-missing", mountBrokenLink, mountUnlink, mountReplace, "file-replace",
}

func mountConfigurations() []mountConfiguration {
	var configurations []mountConfiguration
	for _, version := range []int{1, 2} {
		for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
			for _, codec := range []string{"none", "lz4", mountCodecZstd} {
				c := mountConfiguration{Version: version, Profile: profile, Codec: codec}
				configurations = append(configurations, c)
				if codec == mountCodecZstd {
					c.Dictionary = true
					configurations = append(configurations, c)
				}
			}
		}
	}
	return configurations
}

func mountExpected() []string {
	var ids []string
	for _, c := range mountConfigurations() {
		for _, scenario := range mountScenarios {
			for _, mode := range []string{execModeMemfd, execModeCache} {
				ids = append(ids, c.name()+"/"+scenario+"/"+mode)
			}
		}
	}
	for _, scenario := range []string{mountProcMissing, mountProcInaccessible} {
		ids = append(ids, "native/"+scenario)
	}
	for _, launcher := range []string{execModeNative, launcherFullProfile, launcherMinimalProfile} {
		for _, mode := range []string{execModeMemfd, execModeCache} {
			for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
				for _, layout := range []string{"siblings", "missing-siblings", "generation-switch", mountRemovedGeneration} {
					ids = append(ids, strings.Join([]string{"companions", launcher, mode, profile, layout}, "/"))
				}
			}
		}
	}
	return ids
}

func validateMountSummary(summary mountSummary) error {
	if len(summary.Expected) == 0 || len(summary.Results) != len(summary.Expected) {
		return errors.New("incomplete mount evidence")
	}
	wanted := make(map[string]bool, len(summary.Expected))
	for _, id := range summary.Expected {
		if wanted[id] {
			return errors.New("duplicate expected mount case")
		}
		wanted[id] = true
	}
	for _, result := range summary.Results {
		if !wanted[result.Case] || result.Status != mountPass {
			return fmt.Errorf("missing, duplicate or unsuccessful case %q", result.Case)
		}
		delete(wanted, result.Case)
	}
	return nil
}

func writeMountJSON(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(name, append(data, '\n'), privateFilePerm))
}

func mountCommandOutput(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), mountTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("unavailable: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestMountQualification(t *testing.T) {
	mode := os.Getenv(mountModeEnv)
	if mode == "" {
		t.Skip("native mount qualification is opt-in; run task qualify-mounts")
	}
	require.Contains(t, []string{"auto", mountRequired}, mode)
	backend := os.Getenv(mountBackendEnv)
	if backend == "" {
		backend = "userns"
	}
	require.Contains(t, []string{"userns", mountSudo}, backend)
	output := os.Getenv(mountOutputEnv)
	if output == "" {
		output = "../../.work/mount-qualification"
	}
	output, err := filepath.Abs(output)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(output, privateDirPerm))
	summary := mountSummary{Schema: 1, Status: "incomplete", Backend: backend, Architecture: runtime.GOARCH,
		Source: mountCommandOutput("git", "rev-parse", "HEAD"), Dirty: mountCommandOutput("git", "status", "--porcelain") != "",
		Kernel: mountCommandOutput("uname", "-srmo"), GoVersion: runtime.Version(), PageSize: os.Getpagesize(),
		UnshareVersion: mountCommandOutput(mountUnshare, "--version"), Expected: mountExpected()}
	writeMountJSON(t, filepath.Join(output, "summary.json"), summary)
	defer func() {
		if err := validateMountSummary(summary); err == nil && !t.Failed() {
			summary.Status = mountPass
		}
		writeMountJSON(t, filepath.Join(output, "summary.json"), summary)
	}()
	if reason := mountPreflight(backend); reason != "" {
		for _, id := range summary.Expected {
			summary.Results = append(summary.Results,
				mountEvidence{Schema: 1, Case: id, Status: mountSkip, Reason: reason})
		}
		if mode == mountRequired {
			t.Fatal(reason)
		}
		t.Skip("incomplete qualification: " + reason)
	}
	products := buildMountProducts(t, filepath.Join(output, "products"))
	summary.Products = map[string]string{}
	for _, path := range []string{
		products.controller, products.reporter, products.replacement, products.cli, products.full, products.minimal,
	} {
		summary.Products[filepath.Base(path)] = mountDigest(t, path)
	}
	for _, c := range mountConfigurations() {
		c.Binary = packMountProduct(t, products, output, c)
		c.Digest = mountDigest(t, c.Binary)
		for _, scenario := range mountScenarios {
			for _, executionMode := range []string{execModeMemfd, execModeCache} {
				id := c.name() + "/" + scenario + "/" + executionMode
				t.Run(id, func(t *testing.T) {
					req := prepareMountRequest(t, products, c.Binary, scenario, executionMode)
					entry := mountEvidence{Schema: 1, Case: id, Configuration: c, PayloadSHA256: products.digest,
						Expected: mountExpectation(scenario, executionMode), Request: req, Status: mountFail}
					runMountCase(t, backend, output, products.controller, &summary, &entry, func(result mountfixture.Result) {
						assertMountExecution(t, products, scenario, req, result)
					})
				})
			}
		}
	}
	for _, scenario := range []string{mountProcMissing, mountProcInaccessible} {
		t.Run("native/"+scenario, func(t *testing.T) {
			req := prepareMountRequest(t, products, products.reporter, scenario, execModeNative)
			entry := mountEvidence{Schema: 1, Case: "native/" + scenario, Expected: "raw static control starts without procfs",
				PayloadSHA256: products.digest, Request: req, Status: mountFail}
			runMountCase(t, backend, output, products.controller, &summary, &entry, func(result mountfixture.Result) {
				assertMountExecution(t, products, scenario, req, result)
			})
		})
	}
	runMountedCompanions(t, backend, output, products, &summary)
	require.NoError(t, validateMountSummary(summary))
}

func mountPreflight(backend string) string {
	if os.Getuid() == 0 || os.Getgid() == 0 {
		return "qualification must be invoked by an ordinary user"
	}
	args := mountNamespaceCommand(backend, "true")
	ctx, cancel := context.WithTimeout(context.Background(), mountTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s namespace prerequisite unavailable: %v: %s", backend, err, out)
	}
	return ""
}

func buildMountProducts(t *testing.T, dir string) mountProducts {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, privateDirPerm))
	p := mountProducts{controller: filepath.Join(dir, "mount-runner"), reporter: filepath.Join(dir, "reporter"),
		replacement: filepath.Join(dir, "replacement"), cli: filepath.Join(dir, "microfat"),
		full: filepath.Join(dir, "microfat-stub"), minimal: filepath.Join(dir, "microfat-stub-minimal")}
	env := []string{"CGO_ENABLED=0", envBaselineAMD64, stubEnvARM64}
	for _, item := range []struct{ pkg, out, flags string }{
		{"./testdata/mount_runner", p.controller, "-ldflags=-s -w"},
		{"./testdata/mount_reporter", p.reporter, "-ldflags=-s -w"},
		{"./testdata/mount_reporter", p.replacement, "-ldflags=-s -w -X main.identity=replacement"},
		{cliPackagePath, p.cli, "-ldflags=-s -w"}, {stubPackagePath, p.full, "-ldflags=-s -w"},
		{stubPackagePath, p.minimal, "-tags=minimal"},
	} {
		require.NoError(t, compileBinaryWithFlags(item.pkg, item.out, env, item.flags))
	}
	p.digest = mountDigest(t, p.reporter)
	return p
}

func mountDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func mountLevel() string {
	if runtime.GOARCH == archARM64 {
		return manifestARM64Base
	}
	return "v1"
}

func packMountProduct(t *testing.T, p mountProducts, output string, c mountConfiguration) string {
	t.Helper()
	path := filepath.Join(output, "products", c.name())
	stub := p.full
	if c.Profile == launcherMinimalProfile {
		stub = p.minimal
	}
	args := []string{inputPackCommand, mountArchFlag, runtime.GOARCH, "--stub", stub, "--format-version", strconv.Itoa(c.Version),
		"--compression", c.Codec, "-v", mountLevel() + "=" + p.reporter, "-o", path}
	if c.Dictionary {
		// Dictionary training requires two variants. Both entries deliberately
		// contain the same baseline-compatible reporter so every host executes
		// identical bytes; this fixture does not claim tier specialization.
		second := "v2"
		if runtime.GOARCH == archARM64 {
			second = "v8.1"
		}
		args = append(args, "--dict", "-v", second+"="+p.reporter)
	}
	ctx, cancel := context.WithTimeout(context.Background(), mountTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, p.cli, args...).CombinedOutput()
	require.NoError(t, err, "%s", out)
	verifyFatIntegrity(t, path)
	var index struct {
		Version        int   `json:"version"`
		DictionarySize int64 `json:"dictionary_size"`
		Variants       []struct {
			Compression string `json:"compression"`
		} `json:"variants"`
	}
	inspected, err := exec.CommandContext(ctx, p.cli, "inspect", "--json", path).Output()
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(inspected, &index))
	require.Equal(t, c.Version, index.Version)
	require.Equal(t, c.Dictionary, index.DictionarySize > 0, "dictionary cases must contain an actual shared dictionary")
	require.NotEmpty(t, index.Variants)
	for _, variant := range index.Variants {
		require.Equal(t, c.Codec, variant.Compression)
	}
	return path
}

func mountWrite(t *testing.T, root, name string, data []byte, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), defaultFilePerm))
	require.NoError(t, os.WriteFile(path, data, mode))
}

func mountCopy(t *testing.T, root, name, source string) {
	t.Helper()
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	mountWrite(t, root, name, data, defaultFilePerm)
}

func prepareMountRequest(t *testing.T, p mountProducts, binary, scenario, mode string) mountfixture.Request {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, privateDirPerm))
	namespace, err := os.Readlink("/proc/self/ns/mnt")
	require.NoError(t, err)
	pidNamespace, err := os.Readlink("/proc/self/ns/pid")
	require.NoError(t, err)
	req := mountfixture.Request{Root: root, UID: os.Getuid(), GID: os.Getgid(), ParentNamespace: namespace,
		ParentPIDNamespace: pidNamespace,
		Proc:               "normal", Command: mountPayloadPath, Runs: 1,
		Args: []string{"--exit-42", mountPayloadArgs, "literal\targument"}, Stdin: mountStdin,
		Env: []string{"PATH=/entry", "HOME=/home", "TMPDIR=/tmp", "MICROFAT_EXEC_MODE=" + mode, "MICROFAT_CACHE_DIR=/cache",
			"XDG_CACHE_HOME=/cache", "MOUNT_SENTINEL=preserved", "APP_ASSET_DIR=/assets"}}
	if mode != execModeNative {
		req.Env = append(req.Env, "MICROFAT_ORIGINAL_EXE=/hostile/inherited/hint")
	}
	if mode == execModeCache {
		req.Runs = 2
	}
	for _, dir := range []string{"rootfs/deployment space", "rootfs/proc", "rootfs/entry", "rootfs/cache", "rootfs/assets",
		"rootfs/home", "rootfs/tmp", "source", "assets", execModeCache} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), defaultFilePerm))
	}
	require.NoError(t, os.Chmod(filepath.Join(root, execModeCache), privateDirPerm))
	mountCopy(t, root, "rootfs/control", p.controller)
	mountCopy(t, root, "source/app ", binary)
	mountCopy(t, root, "source/app.next", p.replacement)
	mountWrite(t, root, "source/config.txt", []byte(mountAsset), privateFilePerm)
	mountWrite(t, root, "source/config.next", []byte("replacement neighboring asset"), privateFilePerm)
	mountWrite(t, root, "assets/config.txt", []byte(mountExplicitAsset), privateFilePerm)
	req.Mounts = []mountfixture.Mount{{Source: "source", Target: "/deployment space"},
		{Source: "assets", Target: "/assets", ReadOnly: true}, {Source: execModeCache, Target: "/cache"}}
	if scenario == "file-bind" || scenario == "file-replace" {
		mountWrite(t, root, "rootfs/deployment space/app ", nil, defaultFilePerm)
		mountWrite(t, root, "rootfs/deployment space/config.txt", nil, privateFilePerm)
		req.Mounts[0] = mountfixture.Mount{Source: "source/app ", Target: mountPayloadPath}
		req.Mounts = append(req.Mounts, mountfixture.Mount{Source: "source/config.txt", Target: "/deployment space/config.txt"})
	}
	switch scenario {
	case "symlink", "path", mountBrokenLink:
		target := mountPayloadPath
		if scenario == mountBrokenLink {
			target = "/not-mounted/app"
		}
		require.NoError(t, os.Symlink(target, filepath.Join(root, "rootfs/entry/app-link")))
		req.Command = "/entry/app-link"
		if scenario == "path" {
			req.Command = "app-link"
		}
	case "readonly-deployment":
		req.Mounts[0].ReadOnly = true
	case "readonly-root", "readonly-no-cache":
		req.ReadOnlyRoot, req.Mounts[0].ReadOnly = true, true
		if scenario == "readonly-no-cache" {
			req.Mounts[2].ReadOnly = true
			req.Runs = 1
		}
	case "proc-noexec", mountProcMissing, mountProcInaccessible:
		req.Proc = strings.TrimPrefix(scenario, "proc-")
		if scenario != "proc-noexec" {
			req.Runs = 1
		}
	case "mount-missing":
		req.Mounts = req.Mounts[1:]
		req.Runs = 1
	case mountUnlink:
		req.Pause, req.Runs = true, 1
		req.Remove = []string{"source/app "}
	case mountReplace, "file-replace":
		req.Pause, req.Runs = true, 1
		req.Renames = []mountfixture.Rename{{From: "source/app.next", To: "source/app "},
			{From: "source/config.next", To: "source/config.txt"}}
	}
	return req
}

func mountExpectation(scenario, mode string) string {
	if scenario == mountProcMissing || scenario == mountProcInaccessible {
		return "launcher fails before payload startup"
	}
	if scenario == "mount-missing" || scenario == mountBrokenLink {
		return "launcher cannot start"
	}
	if scenario == "readonly-no-cache" && mode == execModeCache {
		return "cache miss fails without writable storage"
	}
	return "original payload executes with preserved process state"
}

func invokeMountRunner(backend, controller, request string) ([]string, mountfixture.Result, string, error) {
	return invokeMountRunnerTimeout(backend, controller, request, mountTimeout)
}

func mountNamespaceCommand(backend string, command ...string) []string {
	// Namespace init exiting kills every descendant, including new sessions.
	// unshare also kills init if its waiting parent is terminated unexpectedly.
	args := []string{mountUnshare, "--mount", "--pid", "--fork", "--kill-child=SIGKILL", "--mount-proc",
		"--propagation", "private"}
	if backend == mountSudo {
		args = append([]string{mountSudo, "-n", "--"}, args...)
	} else {
		args = append(args, "--user", "--map-current-user", "--keep-caps")
	}
	return append(append(args, "--"), command...)
}

func invokeMountRunnerTimeout(backend, controller, request string, timeout time.Duration) (
	[]string, mountfixture.Result, string, error,
) {
	args := mountNamespaceCommand(backend, controller, request)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	var result mountfixture.Result
	decodeErr := json.Unmarshal(stdout.Bytes(), &result)
	return args, result, stderr.String(), errors.Join(err, ctx.Err(), decodeErr)
}

func runMountCase(t *testing.T, backend, output, controller string, summary *mountSummary,
	entry *mountEvidence, check func(mountfixture.Result)) {
	t.Helper()
	defer func() {
		if !t.Failed() {
			entry.Status = mountPass
		} else if entry.Reason == "" {
			entry.Reason = "qualification assertion failed; see tests.log and recorded process output"
		}
		name := strings.ReplaceAll(entry.Case, "/", "__") + ".json"
		writeMountJSON(t, filepath.Join(output, name), entry)
		summary.Results = append(summary.Results, *entry)
		writeMountJSON(t, filepath.Join(output, "summary.json"), summary)
	}()
	request := filepath.Join(entry.Request.Root, "request.json")
	writeMountJSON(t, request, entry.Request)
	var sourceBefore map[string]string
	if !entry.Request.Pause {
		sourceBefore = cacheTreeSnapshot(t, filepath.Join(entry.Request.Root, "source"))
	}
	start := time.Now()
	var err error
	entry.Command, entry.Result, entry.HelperStderr, err = invokeMountRunner(backend, controller, request)
	entry.Duration = time.Since(start)
	if err != nil {
		entry.Reason = err.Error()
	}
	require.NoError(t, err, "stage=%s error=%s stderr=%s", entry.Result.Stage, entry.Result.Error, entry.HelperStderr)
	require.Equal(t, "complete", entry.Result.Stage)
	require.Equal(t, 1, entry.Result.Schema)
	require.Empty(t, entry.Result.Error)
	require.NotEqual(t, entry.Request.ParentNamespace, entry.Result.Namespace)
	require.NotEmpty(t, entry.Result.PIDNamespace)
	require.NotEqual(t, entry.Request.ParentPIDNamespace, entry.Result.PIDNamespace)
	require.Len(t, entry.Result.Executions, entry.Request.Runs)
	parentNamespace, namespaceErr := os.Readlink("/proc/self/ns/mnt")
	require.NoError(t, namespaceErr)
	require.Equal(t, entry.Request.ParentNamespace, parentNamespace, "parent mount namespace changed")
	if sourceBefore != nil {
		require.Equal(t, sourceBefore, cacheTreeSnapshot(t, filepath.Join(entry.Request.Root, "source")))
	}
	assertMountFlags(t, entry.Request, entry.Result.MountInfo)
	check(entry.Result)
}

func assertMountFlags(t *testing.T, req mountfixture.Request, mountInfo string) {
	t.Helper()
	const pointField, optionsField = 4, 5
	flags := map[string]string{}
	unescape := strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\")
	for line := range strings.SplitSeq(mountInfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) > optionsField {
			flags[unescape.Replace(fields[pointField])] = "," + fields[optionsField] + ","
		}
	}
	jail := filepath.Join(req.Root, "rootfs")
	require.NotEmpty(t, flags[jail], "fixture root must be a real mount")
	if req.ReadOnlyRoot {
		require.Contains(t, flags[jail], ",ro,")
	}
	for _, mount := range req.Mounts {
		actual := flags[filepath.Join(jail, mount.Target)]
		require.NotEmpty(t, actual, "missing mount at %s", mount.Target)
		if mount.ReadOnly {
			require.Contains(t, actual, ",ro,")
		}
	}
	if req.Proc == "noexec" {
		require.Contains(t, flags[filepath.Join(jail, "proc")], ",noexec,")
	}
}

func assertMountExecution(t *testing.T, p mountProducts, scenario string, req mountfixture.Request, result mountfixture.Result) {
	t.Helper()
	native := strings.Contains(strings.Join(req.Env, "\n"), "MICROFAT_EXEC_MODE=native")
	mode := execModeMemfd
	if strings.Contains(strings.Join(req.Env, "\n"), "MICROFAT_EXEC_MODE=cache") {
		mode = execModeCache
	}
	for _, run := range result.Executions {
		if !native && mountExpectation(scenario, mode) != "original payload executes with preserved process state" {
			require.NotZero(t, run.ExitCode, run.Stdout)
			require.Empty(t, run.Started, "payload must not start on an expected failure")
			require.Empty(t, run.Stdout)
			require.NotEmpty(t, run.Stderr)
			switch scenario {
			case mountProcMissing, mountProcInaccessible:
				require.Contains(t, run.Stderr, "reading secure-execution state")
			case "mount-missing", mountBrokenLink:
				require.Contains(t, run.Stderr, "mount-fixture child:")
			default:
				require.Contains(t, run.Stderr, "[microfat] error:")
			}
			continue
		}
		require.Equal(t, expectedExitCode42, run.ExitCode, run.Stderr)
		require.Equal(t, mountStartup, run.Started)
		digest := p.digest
		if native {
			digest = ""
		}
		report, err := decodeMountReport(run.Stdout, digest)
		require.NoError(t, err, run.Stdout)
		require.Equal(t, "original", report.Identity)
		require.Equal(t, run.PID, report.PID)
		require.Equal(t, "/", report.WorkingDirectory)
		require.Equal(t, req.UID, report.UID)
		require.Equal(t, req.UID, report.EUID)
		require.Equal(t, req.GID, report.GID)
		require.Equal(t, req.GID, report.EGID)
		require.Equal(t, append([]string{req.Command}, req.Args...), report.Args)
		require.Equal(t, mountStdin, report.Stdin)
		require.Equal(t, "preserved", report.Environment)
		require.Equal(t, mountExplicitAsset, report.ExplicitAsset)
		if native {
			require.Contains(t, report.Errors, "digest")
			continue
		}
		require.Equal(t, p.digest, report.Digest)
		require.Equal(t, "0000000000000000", report.Capabilities)
		require.Empty(t, report.Errors)
		require.Equal(t, mode, report.Mode)
		require.Equal(t, mountPayloadPath, report.Original)
		require.Equal(t, mountPayloadPath, report.RuntimeExecutable)
		if mode == execModeMemfd {
			require.Contains(t, report.Executable, "memfd:")
		} else {
			require.True(t, strings.HasPrefix(report.Executable, "/cache/"), report.Executable)
		}
		asset := mountAsset
		if scenario == mountReplace {
			asset = "replacement neighboring asset"
		}
		require.Equal(t, asset, report.Asset)
	}
}

func decodeMountReport(stdout, expectedDigest string) (mountfixture.Report, error) {
	var report mountfixture.Report
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return report, err
	}
	if expectedDigest != "" && report.Digest != expectedDigest {
		return report, errors.New("executed payload digest mismatch")
	}
	return report, nil
}
