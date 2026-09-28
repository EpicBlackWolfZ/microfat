//go:build linux

package e2e_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
)

func runMountedCompanions(t *testing.T, backend, output string, p mountProducts, summary *mountSummary) {
	t.Helper()
	packDir := filepath.Join(output, "companions")
	require.NoError(t, os.MkdirAll(filepath.Join(packDir, "products"), privateDirPerm))
	launchers := map[string]string{execModeNative: p.cli}
	cliPayload := p
	cliPayload.reporter = p.cli
	for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
		c := mountConfiguration{Version: 2, Profile: profile, Codec: mountCodecZstd}
		launchers[profile] = packMountProduct(t, cliPayload, packDir, c)
	}
	for _, launcher := range []string{execModeNative, launcherFullProfile, launcherMinimalProfile} {
		for _, mode := range []string{execModeMemfd, execModeCache} {
			for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
				for _, layout := range []string{"siblings", "missing-siblings", "generation-switch", mountRemovedGeneration} {
					id := strings.Join([]string{"companions", launcher, mode, profile, layout}, "/")
					t.Run(id, func(t *testing.T) {
						req := prepareCompanionRequest(t, p, launchers[launcher], mode, profile, layout)
						entry := mountEvidence{Schema: 1, Case: id, Status: mountFail, Request: req,
							Expected: "pack with the original generation companion; missing companions fail without output",
							Configuration: mountConfiguration{Version: 2, Profile: launcher, Codec: mountCodecZstd,
								Binary: launchers[launcher], Digest: mountDigest(t, launchers[launcher])}, PayloadSHA256: mountDigest(t, p.cli)}
						runMountCase(t, backend, output, p.controller, summary, &entry, func(result mountfixture.Result) {
							assertMountedCompanion(t, p, req, profile, layout, result)
						})
					})
				}
			}
		}
	}
}

func prepareCompanionRequest(t *testing.T, p mountProducts, cli, mode, profile, layout string) mountfixture.Request {
	t.Helper()
	req := prepareMountRequest(t, p, cli, "directory", mode)
	req.Runs = 1
	for _, directory := range []string{"rootfs/input", "rootfs/output", "input", manifestKeyOutput} {
		require.NoError(t, os.MkdirAll(filepath.Join(req.Root, directory), defaultFilePerm))
	}
	mountCopy(t, req.Root, "input/reporter", p.reporter)
	req.Mounts = append(req.Mounts, mountfixture.Mount{Source: "input", Target: "/input", ReadOnly: true},
		mountfixture.Mount{Source: manifestKeyOutput, Target: "/output"})
	req.Args = []string{inputPackCommand, mountArchFlag, runtime.GOARCH, "--stub-profile", profile,
		"-v", mountLevel() + "=/input/reporter", "-o", "/output/packed"}
	if layout == "siblings" {
		mountCopy(t, req.Root, "source/microfat-stub", p.full)
		mountCopy(t, req.Root, "source/microfat-stub-minimal", p.minimal)
	}
	if strings.HasPrefix(layout, "generation-") {
		prepareMountedGeneration(t, p, cli, layout, &req)
	}
	return req
}

func prepareMountedGeneration(t *testing.T, p mountProducts, cli, layout string, req *mountfixture.Request) {
	t.Helper()
	const oldID = "11111111111111111111111111111111"
	const newID = "22222222222222222222222222222222"
	store := filepath.Join(req.Root, "store")
	writeManagedGeneration(t, store, oldID, "0.3.0", cli, p.full, p.minimal, nil)
	writeManagedGeneration(t, store, newID, "0.3.1", cli, p.full, p.minimal, []byte("new companion generation"))
	writeMountJSON(t, filepath.Join(store, "owner.json"), map[string]any{"schema": 1, "kind": "microfat-installer",
		"id": strings.Repeat("3", 32), "uid": req.UID, "bin": "/bin links", "store": "/store"})
	require.NoError(t, os.Symlink("generations/"+oldID, filepath.Join(store, "current")))
	require.NoError(t, os.Symlink("generations/"+newID, filepath.Join(store, "next")))
	for _, directory := range []string{"bin", "decoy", "rootfs/bin links", "rootfs/store", "rootfs/decoy"} {
		require.NoError(t, os.MkdirAll(filepath.Join(req.Root, directory), defaultFilePerm))
	}
	for _, product := range []string{"microfat", "microfat-stub", "microfat-stub-minimal"} {
		require.NoError(t, os.Symlink("/store/current/"+product, filepath.Join(req.Root, "bin", product)))
	}
	mountCopy(t, req.Root, "decoy/microfat-stub", filepath.Join(store, "generations", newID, "microfat-stub"))
	mountCopy(t, req.Root, "decoy/microfat-stub-minimal", filepath.Join(store, "generations", newID, "microfat-stub-minimal"))
	req.Mounts = append(req.Mounts, mountfixture.Mount{Source: "store", Target: "/store", ReadOnly: true},
		mountfixture.Mount{Source: "bin", Target: "/bin links", ReadOnly: true},
		mountfixture.Mount{Source: "decoy", Target: "/decoy", ReadOnly: true})
	req.Command = "/bin links/microfat"
	req.Env[0] = "PATH=/bin links:/decoy"
	req.Pause = true
	req.Renames = []mountfixture.Rename{{From: "store/next", To: "store/current"}}
	if layout == mountRemovedGeneration {
		req.Remove = []string{"store/generations/" + oldID}
	}
}

func assertMountedCompanion(t *testing.T, p mountProducts, req mountfixture.Request,
	profile, layout string, result mountfixture.Result) {
	t.Helper()
	run := result.Executions[0]
	packed := filepath.Join(req.Root, "output/packed")
	if layout == "missing-siblings" || layout == mountRemovedGeneration {
		require.NotZero(t, run.ExitCode, run.Stdout)
		require.NoFileExists(t, packed)
		if layout == mountRemovedGeneration {
			require.Contains(t, run.Stderr, "cannot bind companion discovery")
		}
		return
	}
	require.Zero(t, run.ExitCode, run.Stderr)
	stub := p.full
	if profile == launcherMinimalProfile {
		stub = p.minimal
	}
	want, err := os.ReadFile(stub)
	require.NoError(t, err)
	actual, err := os.ReadFile(packed)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(actual, want), "packed output must use the physical G1 companion")
	require.False(t, bytes.HasPrefix(actual, append(want, []byte("new companion generation")...)), "G2 companion selected")
	verifyFatIntegrity(t, packed)
}
