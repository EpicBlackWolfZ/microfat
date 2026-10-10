//go:build linux

package e2e_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

type cacheFirstProfile struct {
	name string
	path string
}

const cacheFirstSymlink = "symlink"

func cacheFirstProfiles(t *testing.T) []cacheFirstProfile {
	t.Helper()
	dir := t.TempDir()
	minimalStub := filepath.Join(dir, "minimal-stub")
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, minimalStub,
		[]string{envBaselineAMD64, "GOARM64=" + manifestARM64Base}, "-tags=minimal", "-ldflags=-s -w"))
	minimalFat := filepath.Join(dir, "minimal-fat")
	require.NoError(t, packBinary(cliPath, minimalStub, "cache-first", minimalFat, goldenVariantBins))
	return []cacheFirstProfile{{name: launcherFullProfile, path: goldenFatBin}, {name: launcherMinimalProfile, path: minimalFat}}
}

func cacheFirstEnv(dir string) []string {
	return []string{
		"MICROFAT_CACHE_DIR=" + dir,
		"MICROFAT_EXEC_MODE=",
		"MICROFAT_DISPATCH_MODE=",
		"MICROFAT_FORCE_LEVEL=",
		"MICROFAT_MAX_LEVEL=",
		"MICROFAT_DISABLE_VARIANTS=",
		"MICROFAT_LOG=",
		"MICROFAT_VERIFY_CACHE=",
		envDebugTrue,
	}
}

func runCacheFirstProcess(t *testing.T, path string, env []string, args ...string) (string, string, error) {
	t.Helper()
	const deadline = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	var stdout, stderr bytes.Buffer
	err := runFixtureCommand(func() *exec.Cmd {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		return cmd
	})
	require.NoError(t, ctx.Err(), "launcher blocked: %s", stderr.String())
	return stdout.String(), stderr.String(), err
}

func requireCacheFirstMode(t *testing.T, path string, env []string, mode string) {
	t.Helper()
	stdout, stderr, err := runCacheFirstProcess(t, path, env, "--echo-env", "MICROFAT_EXEC_MODE")
	require.NoError(t, err, stderr)
	require.Equal(t, mode, stdout, stderr)
	require.Contains(t, stderr, "exec_mode="+mode)
}

func prepareCacheFirstEntry(t *testing.T, path, dir string) string {
	t.Helper()
	env := append(cacheFirstEnv(dir), envExecCache)
	requireCacheFirstMode(t, path, env, execModeCache)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	return filepath.Join(dir, entries[0].Name())
}

func TestCacheFirstAutoExecution(t *testing.T) {
	t.Parallel()
	for _, profile := range cacheFirstProfiles(t) {
		t.Run(profile.name, func(t *testing.T) {
			t.Parallel()
			t.Run("verified warm cache and forced modes", func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				dir := filepath.Join(root, "cache")
				entry := prepareCacheFirstEntry(t, profile.path, dir)
				original, err := os.Stat(entry)
				require.NoError(t, err)
				// A valid hit needs no write access to its directory or entry.
				const readOnlyDirMode = 0o500
				const readOnlyExecMode = 0o500
				require.NoError(t, os.Chmod(entry, readOnlyExecMode))
				require.NoError(t, os.Chmod(dir, readOnlyDirMode))
				t.Cleanup(func() { require.NoError(t, os.Chmod(dir, privateDirPerm)) })
				before := cacheTreeSnapshot(t, root)
				env := cacheFirstEnv(dir)
				requireCacheFirstMode(t, profile.path, env, execModeCache)
				requireCacheFirstMode(t, profile.path, append(env, "MICROFAT_EXEC_MODE=memfd"), execModeMemfd)
				requireCacheFirstMode(t, profile.path, append(env, envExecCache), execModeCache)
				require.Equal(t, before, cacheTreeSnapshot(t, root))
				after, err := os.Stat(entry)
				require.NoError(t, err)
				require.True(t, os.SameFile(original, after), "warm execution replaced the cache inode")
				require.Equal(t, original.ModTime(), after.ModTime())
			})
			for _, state := range []string{"absent", "empty", "insecure-directory"} {
				t.Run("cold miss/"+state, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					dir := filepath.Join(root, "cache")
					if state != "absent" {
						require.NoError(t, os.Mkdir(dir, privateDirPerm))
					}
					if state == "insecure-directory" {
						const insecureMode = 0o777
						require.NoError(t, os.Chmod(dir, insecureMode))
					}
					before := cacheTreeSnapshot(t, root)
					requireCacheFirstMode(t, profile.path, cacheFirstEnv(dir), execModeMemfd)
					require.Equal(t, before, cacheTreeSnapshot(t, root), "cold lookup changed the cache tree")
				})
			}
			t.Run("cold miss with memfd denied materializes cache", func(t *testing.T) {
				t.Parallel()
				dir := filepath.Join(t.TempDir(), "cache")
				stdout, stderr, err := runCacheFirstProcess(t, seccompRunnerPath, cacheFirstEnv(dir),
					profile.path, "--echo-env", "MICROFAT_EXEC_MODE")
				require.NoError(t, err, stderr)
				require.Equal(t, execModeCache, stdout, stderr)
				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				require.Len(t, entries, 1)
			})
		})
	}
}

func TestCacheFirstCorruptEntryMissAndRepair(t *testing.T) {
	t.Parallel()
	for _, profile := range cacheFirstProfiles(t) {
		for _, damage := range []string{"size", "checksum"} {
			for _, verify := range []string{"", "0", "false"} {
				t.Run(profile.name+"/"+damage+"/verify="+verify, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					dir := filepath.Join(root, "cache")
					entry := prepareCacheFirstEntry(t, profile.path, dir)
					original, err := os.ReadFile(entry)
					require.NoError(t, err)
					corrupt := bytes.Clone(original)
					if damage == "size" {
						corrupt = corrupt[:1]
					} else {
						corrupt[0] ^= 0xff
					}
					require.NoError(t, retryFixtureBusy(func() error { return os.WriteFile(entry, corrupt, defaultFilePerm) }))
					before := cacheTreeSnapshot(t, root)
					env := append(cacheFirstEnv(dir), "MICROFAT_VERIFY_CACHE="+verify)
					requireCacheFirstMode(t, profile.path, env, execModeMemfd)
					require.Equal(t, before, cacheTreeSnapshot(t, root), "auto lookup repaired or removed a corrupt entry")
					// Explicit cache retains verified extraction and repair even with bypass attempts.
					requireCacheFirstMode(t, profile.path, append(env, envExecCache), execModeCache)
					repaired, err := os.ReadFile(entry)
					require.NoError(t, err)
					require.Equal(t, original, repaired)
				})
			}
		}
	}
}

func TestCacheFirstUnsafeEntriesFailClosed(t *testing.T) {
	t.Parallel()
	for _, profile := range cacheFirstProfiles(t) {
		for _, kind := range []string{"fifo", "socket", cacheFirstSymlink, "directory", "group-write", "other-write", "setuid"} {
			t.Run(profile.name+"/"+kind, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				dir := filepath.Join(root, "cache")
				entry := prepareCacheFirstEntry(t, profile.path, dir)
				prepareUnsafeCacheFirstEntry(t, entry, kind)
				before := cacheTreeSnapshot(t, root)
				for _, mode := range []string{"", execModeCache} {
					env := append(cacheFirstEnv(dir), "MICROFAT_EXEC_MODE="+mode)
					stdout, stderr, err := runCacheFirstProcess(t, profile.path, env, "--echo-env", "MICROFAT_EXEC_MODE")
					require.Error(t, err, "unsafe entry was accepted: %s", stderr)
					require.Empty(t, stdout, "payload executed despite unsafe cache metadata")
					require.Equal(t, before, cacheTreeSnapshot(t, root), "unsafe entry was repaired or removed")
				}
			})
		}
	}
}

func prepareUnsafeCacheFirstEntry(t *testing.T, entry, kind string) {
	t.Helper()
	switch kind {
	case "fifo", "socket", cacheFirstSymlink, "directory":
		require.NoError(t, os.Remove(entry))
		switch kind {
		case "fifo":
			require.NoError(t, unix.Mkfifo(entry, uint32(privateFilePerm)))
		case "socket":
			dirFD, err := unix.Open(filepath.Dir(entry), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			require.NoError(t, err)
			defer unix.Close(dirFD)
			socketFD, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
			require.NoError(t, err)
			defer unix.Close(socketFD)
			name := "/proc/self/fd/" + strconv.Itoa(dirFD) + "/" + filepath.Base(entry)
			require.NoError(t, unix.Bind(socketFD, &unix.SockaddrUnix{Name: name}))
		case cacheFirstSymlink:
			require.NoError(t, os.Symlink("/bin/true", entry))
		case "directory":
			require.NoError(t, os.Mkdir(entry, privateDirPerm))
		}
	case "group-write":
		const groupWritableMode = 0o720
		require.NoError(t, os.Chmod(entry, groupWritableMode))
	case "other-write":
		const otherWritableMode = 0o702
		require.NoError(t, os.Chmod(entry, otherWritableMode))
	case "setuid":
		require.NoError(t, os.Chmod(entry, os.ModeSetuid|privateDirPerm))
	}
}

func TestCacheFirstWarmHitAndSourceCorruption(t *testing.T) {
	t.Parallel()
	for _, profile := range cacheFirstProfiles(t) {
		t.Run(profile.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "cache")
			entry := prepareCacheFirstEntry(t, profile.path, dir)
			fat := filepath.Join(root, "corrupt-fat")
			copyFile(t, profile.path, fat)
			_, index := readTrailerAndIndex(t, fat)
			found := false
			for _, variant := range index.Variants {
				if variant.SHA256 == filepath.Base(entry) {
					mutateFileBytes(t, fat, variant.Offset, bytes.Repeat([]byte{0xff}, int(trailerMagicSizeBytes)))
					found = true
					break
				}
			}
			require.True(t, found, "cached payload missing from fixture index")
			before := cacheTreeSnapshot(t, root)
			// A verified hit checks executed bytes; it does not decode the compressed source again.
			requireCacheFirstMode(t, fat, cacheFirstEnv(dir), execModeCache)
			for _, env := range [][]string{
				append(cacheFirstEnv(dir), "MICROFAT_EXEC_MODE=memfd"),
				cacheFirstEnv(filepath.Join(root, "absent-cache")),
			} {
				stdout, stderr, err := runCacheFirstProcess(t, fat, env, "--echo-env", "MICROFAT_EXEC_MODE")
				require.Error(t, err, stderr)
				require.Empty(t, stdout, "source corruption must abort extraction without cache fallback")
			}
			require.Equal(t, before, cacheTreeSnapshot(t, root))
		})
	}
}
