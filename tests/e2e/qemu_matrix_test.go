//go:build linux

package e2e_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func (h *qemuHarness) controls(t *testing.T) {
	for _, name := range []string{"explicit-qemu", "binfmt-raw"} {
		t.Run(name, func(t *testing.T) {
			req := prepareQemuRequest(t, h.products, h.products.reporter, execModeNative)
			if name == "explicit-qemu" {
				req.Command, req.Args = "/emulator", append([]string{mountPayloadPath}, req.Args...)
			}
			entry := qemuEntry("controls/"+name, qemuSuccess, req)
			h.run(t, &entry, func(run mountfixture.Execution) {
				assertQemuPayload(t, req, run, h.products.digest, mountPayloadPath)
			})
		})
	}
	for _, storage := range []string{qemuDisk, execModeMemfd} {
		for _, policy := range []string{qemuCloexec, qemuKeep} {
			t.Run("arm64/"+storage+"/"+policy, func(t *testing.T) { h.descriptorControl(t, storage, policy, false) })
		}
		t.Run("native/"+storage+"/"+qemuCloexec, func(t *testing.T) { h.descriptorControl(t, storage, qemuCloexec, true) })
	}
}

func (h *qemuHarness) descriptorControl(t *testing.T, storage, policy string, native bool) {
	t.Helper()
	req := prepareQemuRequest(t, h.products, h.products.reporter, execModeNative)
	arch, digest, outcome := archARM64, h.products.digest, qemuLimitation
	probe, payload := "/deployment space/probe", "/deployment space/raw"
	if native {
		arch, digest = "native", mountDigest(t, h.products.nativeReporter)
		probe, payload = "/deployment space/native-probe", "/deployment space/native-reporter"
	}
	if native || policy == qemuKeep {
		outcome = qemuSuccess
	}
	req.Command, req.Args = probe, append([]string{payload, storage, policy}, req.Args...)
	entry := qemuEntry(strings.Join([]string{"controls", arch, storage, policy}, "/"), outcome, req)
	h.run(t, &entry, func(run mountfixture.Execution) {
		record, err := qemuProbeRecord(run.Stderr)
		require.NoError(t, err, run.Stderr)
		entry.Probe = &record
		require.Equal(t, run.PID, record.PID)
		require.Equal(t, digest, record.Digest)
		require.Equal(t, storage, record.Storage)
		require.Greater(t, record.FD, 3)
		require.NotEmpty(t, record.Target)
		flags := unix.FD_CLOEXEC
		if policy == qemuKeep {
			flags = 0
		}
		require.Equal(t, flags, record.Flags)
		if storage == execModeMemfd {
			const seals = unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
			require.Equal(t, seals, record.Seals)
		}
		if outcome == qemuLimitation {
			require.Greater(t, run.ExitCode, 0, run.Stderr)
			require.Empty(t, run.Started, run.Stdout)
			return
		}
		argv0 := payload
		if !native {
			argv0 = "/proc/self/fd/" + strconv.Itoa(record.FD)
		}
		report := assertQemuPayload(t, req, run, digest, argv0)
		if native {
			require.NotEqual(t, record.Target, report.ProbeFDTarget, "original descriptor must close; its number may be reused")
		} else {
			require.Equal(t, record.Target, report.ProbeFDTarget, "diagnostic keep control must retain its descriptor")
		}
	})
}

func (h *qemuHarness) matrix(t *testing.T, c mountConfiguration) {
	cacheReq := prepareQemuRequest(t, h.products, c.Binary, execModeCache)
	coldComplete := false
	for _, mode := range []string{execModeMemfd, "cache-cold", "cache-warm", qemuAuto} {
		t.Run(mode, func(t *testing.T) {
			dispatch := mode
			if strings.HasPrefix(mode, execModeCache) {
				dispatch = execModeCache
			}
			req := prepareQemuRequest(t, h.products, c.Binary, dispatch)
			if dispatch == execModeCache {
				if mode == "cache-warm" {
					require.True(t, coldComplete, "cold cache case must complete first")
				}
				req = cacheReq
			}
			entry := qemuEntry(c.name()+"/"+mode, qemuLimitation, req)
			entry.Configuration = c
			info, err := os.Stat(h.products.reporter)
			require.NoError(t, err)
			if mode == qemuAuto {
				dispatch = execModeMemfd
			}
			h.run(t, &entry, func(run mountfixture.Execution) {
				var err error
				entry.Dispatch, err = qemuLimitationResult(run, h.products.digest, dispatch, info.Size())
				require.NoError(t, err, "%s\n%s", run.Stdout, run.Stderr)
				if dispatch == execModeMemfd {
					require.Empty(t, entry.CacheBefore)
					require.Empty(t, entry.CacheAfter, "memfd execution cannot populate the cache")
					return
				}
				assertQemuCache(t, req.Root, h.products.digest)
				if mode == "cache-cold" {
					require.Empty(t, entry.CacheBefore)
					coldComplete = true
				} else {
					require.NotEmpty(t, entry.CacheBefore)
					require.Equal(t, entry.CacheBefore, entry.CacheAfter, "warm cache must retain the verified entry")
				}
			})
		})
	}
	t.Run("lifecycle", func(t *testing.T) { h.lifecycle(t, c) })
}

func assertQemuCache(t *testing.T, root, digest string) {
	t.Helper()
	files, err := os.ReadDir(filepath.Join(root, execModeCache))
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, digest, mountDigest(t, filepath.Join(root, execModeCache, files[0].Name())))
}

func (h *qemuHarness) lifecycle(t *testing.T, c mountConfiguration) {
	req := prepareQemuRequest(t, h.products, c.Binary, execModeNative)
	req.Args = []string{"--microfat:optimize-to=/deployment space/extracted"}
	outcome := qemuOptimize
	if c.Profile == launcherMinimalProfile {
		outcome = qemuMinimal
	}
	entry := qemuEntry(c.name()+"/lifecycle", outcome, req)
	entry.Configuration = c
	h.run(t, &entry, func(run mountfixture.Execution) {
		require.Empty(t, run.Started)
		require.Equal(t, c.Digest, mountDigest(t, filepath.Join(req.Root, "source/app ")))
		if c.Profile == launcherMinimalProfile {
			require.Greater(t, run.ExitCode, 0)
			require.Contains(t, run.Stderr, "meta-commands are disabled in minimal launcher stub profile")
			require.NoFileExists(t, filepath.Join(req.Root, "source/extracted"))
			return
		}
		require.Zero(t, run.ExitCode, run.Stderr)
		require.Equal(t, h.products.digest, mountDigest(t, filepath.Join(req.Root, "source/extracted")))
		raw := req
		raw.Command, raw.Args = "/deployment space/extracted", []string{"--exit-42", mountPayloadArgs, "literal\targument"}
		request := filepath.Join(raw.Root, "raw-request.json")
		writeMountJSON(t, request, raw)
		command, result, stderr, err := invokeMountArgs(qemuNamespaceCommand(h.backend, h.products.controller, request), qemuControllerTimeout)
		writeMountJSON(t, filepath.Join(h.output, c.name()+"__extracted.json"), struct {
			Request mountfixture.Request `json:"request"`
			Command []string             `json:"command"`
			Result  mountfixture.Result  `json:"result"`
		}{raw, command, result})
		require.NoError(t, err, "%s %s", stderr, result.Error)
		require.Len(t, result.Executions, 1)
		assertQemuPayload(t, raw, result.Executions[0], h.products.digest, raw.Command)
	})
}
