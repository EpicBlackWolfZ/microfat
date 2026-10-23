package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"golang.org/x/sys/unix"
)

const (
	nativeLaunchTimeout = 10 * time.Second
	nativeLaunchGrace   = time.Second
	nativeLaunchLimit   = 64 << 10
	nativeLaunchMode    = "native"
	nativeLaunchKind    = "untimed-native-correctness"
)

type launchObservation struct {
	Readiness         nativeReadiness `json:"readiness"`
	MeasurementKind   string          `json:"measurement_kind"`
	RequestedMode     string          `json:"requested_mode"`
	ExecMode          string          `json:"exec_mode"`
	ExpectedVariant   string          `json:"expected_variant"`
	ExpectedSHA256    string          `json:"expected_sha256"`
	ExpectedSizeBytes int64           `json:"expected_size_bytes"`
	ExpectedArch      string          `json:"expected_arch,omitempty"`
	SelectedVariant   string          `json:"selected_variant"`
	SelectedSHA256    string          `json:"selected_sha256"`
	SelectedSizeBytes int64           `json:"selected_size_bytes"`
	NativePayloadPath string          `json:"native_payload_path"`
	ArtifactPath      string          `json:"artifact_path"`
	CacheDir          string          `json:"private_cache_dir,omitempty"`
	Prewarmed         bool            `json:"prewarmed"`
	OutputMatched     bool            `json:"output_matched"`
	Stdout            string          `json:"stdout"`
	Stderr            string          `json:"stderr"`
	Error             string          `json:"error,omitempty"`
}

type nativeLaunchRunner func(context.Context, string, []string, []string) (string, string, error)

type nativeLaunchHooks struct {
	host    func() microarch.Info
	run     nativeLaunchRunner
	prewarm func(string, string, string) error
}

// verifyNativeLaunches checks allowlisted, trusted, terminating corpus fixtures.
// It runs outside the measured packing cgroup and has no latency claim.
// Dispatch telemetry reports the actual mode, selected bytes and variant; these
// checks do not replace the launcher's descriptor-bound security implementation.
func verifyNativeLaunches(ctx context.Context, artifact string, m corpusManifest, family, dir string) ([]launchObservation, error) {
	return verifyNativeLaunchesWithHooks(ctx, artifact, m, family, dir, nativeLaunchHooks{
		host: microarch.Detect, run: runNativeLaunch, prewarm: prewarmNativeLaunch,
	})
}

func nativeFixtureArgs(family string) ([]string, error) {
	switch family {
	case familyDemo:
		return []string{"--startup-only", "--json"}, nil
	case familyMicrofat:
		return []string{"--version"}, nil
	case familyTask:
		return []string{"--version"}, nil
	case familyFortio:
		return []string{"version"}, nil
	case familyActionlint, familyGovulncheck:
		return []string{"-version"}, nil
	default:
		return nil, errors.New("native execution requires an allowlisted trusted corpus fixture")
	}
}

func selectNativeFixture(m corpusManifest, family string, host microarch.Info) (corpusVariant, error) {
	if host.OS != "linux" || host.Arch != corpusArchitecture(m) {
		return corpusVariant{}, errors.New("native correctness execution requires a matching native Linux host")
	}
	var levels []string
	byLevel := map[string]corpusVariant{}
	for _, v := range m.Variants {
		if v.Family != family {
			continue
		}
		if !validCorpusLevel(host.Arch, v.Level) {
			return corpusVariant{}, errors.New("native fixture uses an unsupported corpus level")
		}
		if _, duplicate := byLevel[v.Level]; duplicate {
			return corpusVariant{}, errors.New("duplicate native fixture level")
		}
		levels = append(levels, v.Level)
		byLevel[v.Level] = v
	}
	selection, err := microarch.SelectVariantForHost(host.Arch, host, levels, microarch.Policy{})
	if err != nil {
		return corpusVariant{}, err
	}
	return byLevel[selection.SelectedVariant], nil
}

func verifyNativeLaunchesWithHooks(
	ctx context.Context, artifact string, m corpusManifest, family, dir string, hooks nativeLaunchHooks,
) (observations []launchObservation, returnErr error) {
	if _, err := nativeFixtureArgs(family); err != nil {
		return nil, err
	}
	fixture, err := selectNativeFixture(m, family, hooks.host())
	if err != nil {
		return nil, err
	}
	hash, size, err := digestFile(fixture.Path, maxPortableInput)
	if err != nil {
		return nil, err
	}
	if hash != fixture.SHA256 || size != fixture.Bytes {
		return nil, errors.New("native fixture changed since manifest verification")
	}
	cacheRoot, err := os.MkdirTemp(dir, ".native-launch-check-*")
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, os.RemoveAll(cacheRoot)) }()
	readiness, err := prepareNativeReadiness(family, cacheRoot)
	if err != nil {
		return nil, err
	}
	base := launchObservation{MeasurementKind: nativeLaunchKind, ExpectedVariant: fixture.Level,
		ExpectedSHA256: hash, ExpectedSizeBytes: size, ExpectedArch: corpusArchitecture(m),
		NativePayloadPath: fixture.Path, ArtifactPath: artifact, Readiness: readiness}
	native := base
	native.RequestedMode, native.ExecMode = nativeLaunchMode, nativeLaunchMode
	native.SelectedVariant, native.SelectedSHA256, native.SelectedSizeBytes = fixture.Level, hash, size
	native.Stdout, native.Stderr, err = runNativeReadiness(ctx, fixture.Path, readiness, nativeLaunchEnv("", ""), hooks.run)
	if err == nil {
		err = validateNativeFixtureOutput(family, native.Stdout)
	}
	native.OutputMatched = err == nil
	if err != nil {
		native.Error = err.Error()
		return append(observations, native), err
	}
	observations = append(observations, native)
	for _, mode := range []string{format.ExecModeMemfd, format.ExecModeCache, format.ExecModeAuto} {
		observation := base
		observation.RequestedMode, observation.CacheDir = mode, filepath.Join(cacheRoot, mode)
		if err := os.Mkdir(observation.CacheDir, privateDirMode); err != nil {
			return observations, err
		}
		if mode == format.ExecModeAuto {
			if err := hooks.prewarm(artifact, fixture.Level, observation.CacheDir); err != nil {
				observation.Error = err.Error()
				return append(observations, observation), err
			}
			observation.Prewarmed = true
		}
		observation.Stdout, observation.Stderr, err = runNativeReadiness(ctx, artifact, readiness,
			nativeLaunchEnv(mode, observation.CacheDir), hooks.run)
		if err == nil {
			err = validateNativeDispatch(&observation, native.Stdout)
		}
		if err != nil {
			observation.Error = err.Error()
			return append(observations, observation), err
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

func validateNativeFixtureOutput(family, stdout string) error {
	if family == familyDemo {
		var ready struct {
			Status  string `json:"status"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(stdout), &ready); err != nil {
			return fmt.Errorf("native demo readiness: %w", err)
		}
		if ready.Status != "ready" || ready.Version == "" {
			return errors.New("native demo did not report readiness and version")
		}
		return nil
	}
	if family != familyMicrofat {
		if _, err := nativeFixtureArgs(family); err != nil {
			return err
		}
		if strings.TrimSpace(stdout) == "" {
			return errors.New("native fixture emitted no version output")
		}
		return nil
	}
	if !strings.HasPrefix(stdout, "Version: ") ||
		(!strings.Contains(stdout, "linux/amd64") && !strings.Contains(stdout, "linux/arm64")) {
		return errors.New("native microfat did not report its expected version/platform")
	}
	return nil
}

func validateNativeDispatch(observation *launchObservation, expected string) error {
	var dispatch *format.DispatchTelemetry
	for _, line := range strings.Split(observation.Stderr, "\n") {
		data, found := strings.CutPrefix(line, "[microfat] ")
		if !found {
			continue
		}
		var value format.DispatchTelemetry
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			return fmt.Errorf("dispatch diagnostic JSON: %w", err)
		}
		if value.Event != format.EventDispatch || dispatch != nil {
			return errors.New("expected exactly one successful dispatch diagnostic")
		}
		dispatch = &value
	}
	if dispatch == nil {
		return errors.New("missing actual dispatch diagnostic")
	}
	observation.ExecMode = dispatch.ExecMode
	observation.SelectedVariant = dispatch.SelectedVariant
	observation.SelectedSHA256 = dispatch.SelectedSHA256
	observation.SelectedSizeBytes = dispatch.SelectedSizeBytes
	wantMode := observation.RequestedMode
	if wantMode == format.ExecModeAuto {
		wantMode = format.ExecModeCache
	}
	wantArch := observation.ExpectedArch
	if wantArch == "" {
		wantArch = microarch.ArchAMD64
	}
	if dispatch.ExecMode != wantMode || dispatch.HostArch != wantArch ||
		dispatch.SelectedVariant != observation.ExpectedVariant || dispatch.SelectedSHA256 != observation.ExpectedSHA256 ||
		dispatch.SelectedSizeBytes != observation.ExpectedSizeBytes {
		return errors.New("dispatch mode, host or selected payload identity does not match the requested correctness case")
	}
	observation.OutputMatched = observation.Stdout == expected
	if !observation.OutputMatched {
		return errors.New("fat artifact stdout differs from direct native payload stdout")
	}
	return nil
}

func nativeLaunchEnv(mode, cacheDir string) []string {
	env := []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "GOMAXPROCS=4", "GOVERSION=" + researchGoVersion}
	if mode != "" {
		env = append(env, format.EnvExecMode+"="+mode, format.EnvLog+"=json", format.EnvCacheDir+"="+cacheDir)
	}
	return env
}

func prewarmNativeLaunch(artifact, level, cacheDir string) (returnErr error) {
	file, err := inputfile.Open(artifact)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	_, results, err := pack.PrewarmBinary(file, info.Size(), []string{level}, cacheDir)
	if err != nil {
		return err
	}
	if len(results) != 1 || !results[0].Valid {
		return errors.New("prewarm did not verify the selected cache payload")
	}
	return nil
}

func runNativeLaunch(ctx context.Context, command string, args, env []string) (string, string, error) {
	return runNativeLaunchWithTimeout(ctx, command, args, env, nativeLaunchTimeout)
}

func runNativeLaunchWithTimeout(
	ctx context.Context, command string, args, env []string, timeout time.Duration,
) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...) // #nosec G204 -- explicit opt-in, hash-pinned trusted native fixtures only.
	cmd.Env, cmd.SysProcAttr = env, &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
		if errors.Is(err, unix.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = nativeLaunchGrace
	stdout, stderr := &nativeLaunchOutput{}, &nativeLaunchOutput{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if err != nil && cmd.Process != nil {
		// A failing child can leave descendants holding its pipes. Only signal
		// the new process group owned by this invocation, never a foreign PID.
		killErr := unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
		if killErr != nil && !errors.Is(killErr, unix.ESRCH) {
			err = errors.Join(err, killErr)
		}
	}
	if stdout.truncated || stderr.truncated {
		err = errors.Join(err, errors.New("native launch stdout or stderr exceeds capture bound"))
	}
	return string(stdout.data), string(stderr.data), errors.Join(err, ctx.Err())
}

type nativeLaunchOutput struct {
	data      []byte
	truncated bool
}

var _ io.Writer = (*nativeLaunchOutput)(nil)

func (output *nativeLaunchOutput) Write(data []byte) (int, error) {
	kept := min(len(data), nativeLaunchLimit-len(output.data))
	output.data = append(output.data, data[:kept]...)
	output.truncated = output.truncated || kept != len(data)
	return len(data), nil
}
