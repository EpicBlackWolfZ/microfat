//go:build linux

package runtimequalify

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"golang.org/x/sys/unix"
)

const payloadPath = "/deployment/app"
const unsafeFixtureMode = 0o777

func (c *controller) request(item Case, lineage Lineage) (mountfixture.Request, error) {
	req := mountfixture.Request{UID: os.Getuid(), GID: os.Getgid(), Proc: "normal", Command: payloadPath, Runs: item.Runs,
		Args: []string{"--exit-42", "argument with spaces", "literal\targument"}, Stdin: "runtime qualification stdin\n",
		Env: []string{"PATH=/entry", "HOME=/home", "TMPDIR=/tmp", "MICROFAT_EXEC_MODE=" + item.Mode, "MICROFAT_CACHE_DIR=/cache",
			"MICROFAT_LOG=json", "MOUNT_SENTINEL=preserved", "APP_ASSET_DIR=/assets", "RUNTIME_QUALIFY=1", "GOMAXPROCS=2"},
		Runtime: &mountfixture.Runtime{Workers: item.Workers, Policy: item.Policy}}
	var err error
	req.ParentNamespace, err = os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		return req, err
	}
	req.ParentPIDNamespace, err = os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return req, err
	}
	fixtures := filepath.Join(c.options.Output, "fixtures")
	if err := os.MkdirAll(fixtures, privateMode); err != nil {
		return req, err
	}
	req.Root, err = os.MkdirTemp(fixtures, "case-")
	if err != nil {
		return req, err
	}
	for _, dir := range []string{"rootfs/deployment", "rootfs/proc", "rootfs/entry", "rootfs/cache", "rootfs/assets",
		"rootfs/home", "rootfs/tmp", "source", "assets", "cache"} {
		if err := os.MkdirAll(filepath.Join(req.Root, dir), privateMode); err != nil {
			return req, err
		}
	}
	req.Mounts = []mountfixture.Mount{{Source: "source", Target: "/deployment"},
		{Source: "assets", Target: "/assets", ReadOnly: true}, {Source: "cache", Target: "/cache"}}
	if err := copyFile(c.helper, filepath.Join(req.Root, "rootfs/control"), privateMode, true); err != nil {
		return req, err
	}
	modified := strings.HasPrefix(item.Scenario, corruptPayload) || item.Scenario == corruptDictionary || item.Scenario == capability
	app := filepath.Join(req.Root, "source/app")
	if err := copyFile(lineage.Bundle, app, privateMode, !modified); err != nil {
		return req, err
	}
	for _, name := range []string{"source/config.txt", "assets/config.txt"} {
		if err := os.WriteFile(filepath.Join(req.Root, name), []byte("qualification asset"), dataMode); err != nil {
			return req, err
		}
	}
	if item.Scenario == distributedCLI {
		req.Args = []string{"detect", "--json"}
		return req, nil
	}
	if item.Scenario == policyControl {
		req.Args = []string{"--policy-probe"}
		return req, nil
	}
	return req, configureScenario(c, item, lineage, &req, app)
}

func configureScenario(c *controller, item Case, lineage Lineage, req *mountfixture.Request, app string) error {
	cacheImage := filepath.Join(req.Root, "cache", lineage.PayloadSHA256)
	switch item.Scenario {
	case Full, noExec, readOnly:
		req.Runtime.Cache = item.Scenario
	case readOnlyWarm:
		req.Runtime.Cache = readOnly
	case procNoexec, procMissing, procInaccessible:
		req.Proc = strings.TrimPrefix(item.Scenario, "proc-")
	case busy:
		req.Runtime.BusyName = lineage.PayloadSHA256
	case "signal":
		req.Args[0] = "--signal-term"
	case capability:
		req.Runtime.Capability = true
	}
	switch item.Scenario {
	case readOnlyWarm, busy, corruptCache, insecure:
		if err := copyFile(c.reporter, cacheImage, privateMode, false); err != nil {
			return err
		}
	case corruptPayload, corruptDictionary:
		// A warm valid cache proves auto does not fall back after payload corruption.
		if item.Mode == Auto {
			if err := copyFile(c.reporter, cacheImage, privateMode, false); err != nil {
				return err
			}
		}
		return corruptBundle(app, item.Scenario == corruptDictionary)
	case symlink:
		return os.Symlink("/deployment/app", cacheImage)
	case fifo:
		return unix.Mkfifo(cacheImage, privateMode)
	}
	switch item.Scenario {
	case corruptCache:
		return os.WriteFile(cacheImage, []byte("invalid cache image"), privateMode)
	case insecure:
		// #nosec G302 -- deliberately unsafe private fixture used to prove rejection.
		return os.Chmod(cacheImage, unsafeFixtureMode)
	}
	return nil
}

func corruptBundle(path string, dictionary bool) error {
	// #nosec G304 -- separately copied, private corruption fixture.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	index, err := format.ReadTrailerAndIndex(file, stat.Size())
	if err != nil {
		return err
	}
	var offsets []int64
	if dictionary {
		if index.DictionarySize == 0 {
			return errors.New("missing dictionary fixture")
		}
		offsets = append(offsets, index.DictionaryOffset)
	} else {
		for _, variant := range index.Variants {
			offsets = append(offsets, variant.Offset+variant.CompressedSize/2)
		}
	}
	for _, offset := range offsets {
		var value [1]byte
		if _, err := file.ReadAt(value[:], offset); err != nil {
			return err
		}
		value[0] ^= 0xff
		if _, err := file.WriteAt(value[:], offset); err != nil {
			return err
		}
	}
	return nil
}
