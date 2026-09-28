// Mount reporter is a static, ordinary payload. It never trusts dispatch
// metadata to establish its own identity.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/runtimeinit"
	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
)

var identity = "original"

const (
	startupFD       = 3
	inputLimit      = 4096
	payloadExitCode = 42
	privateFileMode = 0o600
	probeLifetime   = time.Minute
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--detached-child" {
		hang("/cache/descendant.json")
		return
	}
	started := os.NewFile(startupFD, "startup-report")
	if started != nil {
		if _, err := io.WriteString(started, "payload-started\n"); err != nil {
			panic(err)
		}
		if err := started.Close(); err != nil {
			panic(err)
		}
	}
	if len(os.Args) > 1 && (os.Args[1] == "--hang" || os.Args[1] == "--spawn-detached") {
		if os.Args[1] == "--spawn-detached" {
			cmd := exec.Command("/proc/self/exe", "--detached-child")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				panic(err)
			}
		}
		hang("/cache/pid.json")
		return
	}
	r := mountfixture.Report{Identity: identity, PID: os.Getpid(), UID: os.Getuid(), EUID: os.Geteuid(),
		GID: os.Getgid(), EGID: os.Getegid(), Args: os.Args, Environment: os.Getenv("MOUNT_SENTINEL"),
		Mode: os.Getenv("MICROFAT_EXEC_MODE"), Original: os.Getenv("MICROFAT_ORIGINAL_EXE"), Errors: map[string]string{}}
	record := func(key string, err error) {
		if err != nil {
			r.Errors[key] = err.Error()
		}
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, inputLimit))
	record("stdin", err)
	r.Stdin = string(data)
	r.WorkingDirectory, err = os.Getwd()
	record("working_directory", err)
	r.Executable, err = os.Executable()
	record("executable", err)
	r.RuntimeExecutable, err = runtimeinit.Executable()
	record("runtime_executable", err)
	image, err := os.Open("/proc/self/exe")
	if err == nil {
		hash := sha256.New()
		_, err = io.Copy(hash, image)
		record("digest", err)
		record("close", image.Close())
		r.Digest = fmt.Sprintf("%x", hash.Sum(nil))
	} else {
		record("digest", err)
	}
	data, err = os.ReadFile(filepath.Join(filepath.Dir(r.RuntimeExecutable), "config.txt"))
	record("asset", err)
	r.Asset = string(data)
	// #nosec G703 -- isolated test payload reads its caller-selected fixture asset directory.
	data, err = os.ReadFile(filepath.Join(os.Getenv("APP_ASSET_DIR"), "config.txt"))
	record("explicit_asset", err)
	r.ExplicitAsset = string(data)
	data, err = os.ReadFile("/proc/self/status")
	record("status", err)
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "CapEff:") {
			r.Capabilities = strings.TrimSpace(strings.TrimPrefix(line, "CapEff:"))
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
		panic(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "--exit-42" {
		os.Exit(payloadExitCode)
	}
}

func hang(path string) {
	namespace, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		panic(err)
	}
	data, err := json.Marshal(mountfixture.Process{PID: os.Getpid(), Namespace: namespace})
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, data, privateFileMode); err != nil {
		panic(err)
	}
	// Bound even a broken regression's probe; the harness also retains pidfds
	// for independent cleanup on assertion failures.
	time.Sleep(probeLifetime)
}
