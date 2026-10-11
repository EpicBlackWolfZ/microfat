//go:build linux

package benchmarkcleanup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

type fixture struct {
	env       environment
	root      string
	receipt   string
	signalled int
}

const fixtureRun = "123"
const fixtureAttempt = "2"
const unownedRoot = "/unowned"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	cgroups := filepath.Join(base, "cgroups")
	require.NoError(t, os.Mkdir(cgroups, 0o755))
	f := &fixture{root: filepath.Join(cgroups, "microfat-ci-123-2-456")}
	f.env = environment{cgroups: cgroups, receipts: filepath.Join(base, "receipts"), privilegedUID: uint32(os.Getuid()),
		statfs:     func(int) (int64, error) { return unix.CGROUP2_SUPER_MAGIC, nil },
		selfCgroup: func() ([]byte, error) { return []byte("0::/outside\n"), nil },
		openPID:    func(int) (int, error) { return unix.Open("/dev/null", unix.O_RDONLY, 0) },
		signalPID:  func(int) error { f.signalled++; return nil }, unlinkAt: virtualUnlink}
	f.group(t, f.root)
	f.group(t, filepath.Join(f.root, "manager"))
	f.group(t, filepath.Join(f.root, "microfat-bench-1"))
	require.NoError(t, f.env.register(f.root, fixtureRun, fixtureAttempt, uint32(os.Getuid())))
	f.receipt = filepath.Join(f.env.receipts, filepath.Base(f.root)+".json")
	return f
}

func (f *fixture) group(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.Mkdir(path, 0o755))
	for name, data := range map[string]string{"cgroup.type": "domain\n", "cgroup.events": "populated 0\nfrozen 0\n",
		"cgroup.procs": "", "cgroup.kill": ""} {
		writeControl(t, filepath.Join(path, name), data)
	}
}

func writeControl(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(data), receiptMode))
}

// Ordinary fixture files stand in for virtual cgroup controls, which do not prevent kernel rmdir.
func virtualUnlink(fd int, name string, flags int) error {
	if flags == unix.AT_REMOVEDIR {
		parent, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(fd))
		if err != nil {
			return err
		}
		for _, control := range []string{"cgroup.type", "cgroup.events", "cgroup.procs", "cgroup.kill"} {
			if err := os.Remove(filepath.Join(parent, name, control)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return unix.Unlinkat(fd, name, flags)
}

func (f *fixture) open(t *testing.T) *tree {
	t.Helper()
	value, err := f.env.open(f.root, fixtureRun, fixtureAttempt)
	require.NoError(t, err)
	t.Cleanup(value.close)
	return value
}

func (f *fixture) changeReceipt(t *testing.T, change func(*receipt)) {
	t.Helper()
	data, err := os.ReadFile(f.receipt)
	require.NoError(t, err)
	var value receipt
	require.NoError(t, json.Unmarshal(data, &value))
	change(&value)
	data, err = json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.receipt, data, receiptMode))
}

func TestRefusesUnownedDelegations(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*testing.T, *fixture){
		"missing receipt": func(t *testing.T, f *fixture) { require.NoError(t, os.Remove(f.receipt)) },
		"symlink receipt": func(t *testing.T, f *fixture) {
			require.NoError(t, os.Rename(f.receipt, f.receipt+".other"))
			require.NoError(t, os.Symlink(f.receipt+".other", f.receipt))
		},
		"receipt hard link": func(t *testing.T, f *fixture) { require.NoError(t, os.Link(f.receipt, f.receipt+".other")) },
		"writable receipt":  func(t *testing.T, f *fixture) { require.NoError(t, os.Chmod(f.receipt, 0o644)) },
		"writable registry": func(t *testing.T, f *fixture) { require.NoError(t, os.Chmod(f.env.receipts, 0o755)) },
		"symlink registry": func(t *testing.T, f *fixture) {
			require.NoError(t, os.Rename(f.env.receipts, f.env.receipts+".other"))
			require.NoError(t, os.Symlink(f.env.receipts+".other", f.env.receipts))
		},
		"wrong registry owner":  func(_ *testing.T, f *fixture) { f.env.privilegedUID++ },
		"malformed receipt":     func(t *testing.T, f *fixture) { writeControl(t, f.receipt, "{") },
		"trailing receipt":      func(t *testing.T, f *fixture) { writeControl(t, f.receipt, "{} {}") },
		"unknown receipt field": func(t *testing.T, f *fixture) { writeControl(t, f.receipt, `{"extra":1}`) },
		"oversized receipt":     func(t *testing.T, f *fixture) { writeControl(t, f.receipt, string(make([]byte, maxControlBytes+1))) },
		"wrong root":            func(t *testing.T, f *fixture) { f.changeReceipt(t, func(r *receipt) { r.Root += "other" }) },
		"wrong run":             func(t *testing.T, f *fixture) { f.changeReceipt(t, func(r *receipt) { r.RunID = "124" }) },
		"wrong attempt":         func(t *testing.T, f *fixture) { f.changeReceipt(t, func(r *receipt) { r.Attempt = "3" }) },
		"wrong inode":           func(t *testing.T, f *fixture) { f.changeReceipt(t, func(r *receipt) { r.Inode++ }) },
		"wrong device":          func(t *testing.T, f *fixture) { f.changeReceipt(t, func(r *receipt) { r.Device++ }) },
		"wrong root owner":      func(t *testing.T, f *fixture) { f.changeReceipt(t, func(r *receipt) { r.UID++ }) },
		"symlink root": func(t *testing.T, f *fixture) {
			require.NoError(t, os.Rename(f.root, f.root+"other"))
			require.NoError(t, os.Symlink(f.root+"other", f.root))
		},
		"writable root":       func(t *testing.T, f *fixture) { require.NoError(t, os.Chmod(f.root, 0o777)) },
		"ordinary filesystem": func(_ *testing.T, f *fixture) { f.env.statfs = nativeEnvironment().statfs },
		"filesystem error": func(_ *testing.T, f *fixture) {
			f.env.statfs = func(int) (int64, error) { return 0, errors.New("statfs failure") }
		},
		"threaded root": func(t *testing.T, f *fixture) { writeControl(t, filepath.Join(f.root, "cgroup.type"), "threaded\n") },
		"missing type":  func(t *testing.T, f *fixture) { require.NoError(t, os.Remove(filepath.Join(f.root, "cgroup.type"))) },
		"unknown leaf":  func(t *testing.T, f *fixture) { f.group(t, filepath.Join(f.root, "unowned")) },
		"symlink leaf": func(t *testing.T, f *fixture) {
			require.NoError(t, os.Symlink(f.root, filepath.Join(f.root, "microfat-bench-2")))
		},
		"writable leaf": func(t *testing.T, f *fixture) { require.NoError(t, os.Chmod(filepath.Join(f.root, "manager"), 0o777)) },
		"self membership": func(_ *testing.T, f *fixture) {
			f.env.selfCgroup = func() ([]byte, error) { return []byte("0::/" + filepath.Base(f.root) + "/manager\n"), nil }
		},
		"unreadable membership": func(_ *testing.T, f *fixture) {
			f.env.selfCgroup = func() ([]byte, error) { return nil, errors.New("membership failure") }
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			mutate(t, f)
			_, err := f.env.open(f.root, fixtureRun, fixtureAttempt)
			require.Error(t, err)
			require.Zero(t, f.signalled)
			assert.FileExists(t, filepath.Join(f.root, "manager", "cgroup.procs"))
		})
	}
}

func TestExactNameAndOutside(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, root := range []string{f.root + "other", f.root + "/", f.root + "/manager", f.root + "/../" + filepath.Base(f.root),
		"/sys/fs/cgroup", filepath.Join(f.env.cgroups, "microfat-ci-123-2-0"), filepath.Join(f.env.cgroups, "microfat-ci-0123-2-4")} {
		_, err := f.env.open(root, fixtureRun, fixtureAttempt)
		require.Error(t, err, root)
	}
	for _, identity := range [][2]string{{"124", fixtureAttempt}, {fixtureRun, "3"}, {"", ""}} {
		_, err := f.env.open(f.root, identity[0], identity[1])
		require.Error(t, err)
	}
	for _, membership := range []string{"", "1:memory:/", "0::relative", "0::/owned", "0::/owned/manager", "0::/a\n0::/b"} {
		require.Error(t, outside([]byte(membership), "/owned"), membership)
	}
	require.NoError(t, outside([]byte("0::/owned-sibling\n"), "/owned"))
	require.Error(t, f.env.register(f.root, fixtureRun, fixtureAttempt, uint32(os.Getuid())), "exclusive receipt")
}

func TestKernelControlAndSignalContracts(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "populated", "populated 2", "populated 0\npopulated 1", "populated 0 trailing"} {
		f := newFixture(t)
		group := f.open(t)
		writeControl(t, filepath.Join(f.root, "cgroup.events"), value)
		_, err := group.populated()
		require.Error(t, err)
	}
	for name, configure := range map[string]func(*testing.T, *fixture){
		"term":              func(_ *testing.T, _ *fixture) {},
		"pid gone":          func(_ *testing.T, f *fixture) { f.env.openPID = func(int) (int, error) { return -1, unix.ESRCH } },
		"pidfd unavailable": func(_ *testing.T, f *fixture) { f.env.openPID = func(int) (int, error) { return -1, unix.ENOSYS } },
		"signal gone":       func(_ *testing.T, f *fixture) { f.env.signalPID = func(int) error { return unix.ESRCH } },
		"recycled pid": func(t *testing.T, f *fixture) {
			f.env.openPID = func(int) (int, error) {
				writeControl(t, filepath.Join(f.root, "manager", "cgroup.procs"), "")
				return unix.Open("/dev/null", unix.O_RDONLY, 0)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			writeControl(t, filepath.Join(f.root, "manager", "cgroup.procs"), "42\n")
			configure(t, f)
			group := f.open(t)
			require.NoError(t, group.terminate())
			if name == "term" {
				require.Equal(t, 1, f.signalled)
			} else {
				require.Zero(t, f.signalled)
			}
			require.NoError(t, group.force())
			data, err := os.ReadFile(filepath.Join(f.root, "cgroup.kill"))
			require.NoError(t, err)
			require.Equal(t, "1", string(data))
		})
	}
}

func TestDrainRetainsFailedEvidenceAndReceipt(t *testing.T) {
	t.Parallel()
	failure := errors.New("injected failure")
	for name, configure := range map[string]func(*testing.T, *fixture){
		"bad pid": func(t *testing.T, f *fixture) {
			writeControl(t, filepath.Join(f.root, "manager", "cgroup.procs"), "-1")
		},
		"pidfd denied":  func(_ *testing.T, f *fixture) { f.env.openPID = func(int) (int, error) { return -1, failure } },
		"signal denied": func(_ *testing.T, f *fixture) { f.env.signalPID = func(int) error { return failure } },
		"missing procs": func(t *testing.T, f *fixture) {
			require.NoError(t, os.Remove(filepath.Join(f.root, "manager", "cgroup.procs")))
		},
		"missing events": func(t *testing.T, f *fixture) { require.NoError(t, os.Remove(filepath.Join(f.root, "cgroup.events"))) },
		"missing kill":   func(t *testing.T, f *fixture) { require.NoError(t, os.Remove(filepath.Join(f.root, "cgroup.kill"))) },
		"oversized procs": func(t *testing.T, f *fixture) {
			writeControl(t, filepath.Join(f.root, "manager", "cgroup.procs"), string(make([]byte, maxControlBytes+1)))
		},
		"remove denied": func(t *testing.T, f *fixture) {
			writeControl(t, filepath.Join(f.root, "cgroup.events"), "populated 0\n")
			f.env.unlinkAt = func(int, string, int) error { return failure }
		},
		"forced timeout": func(_ *testing.T, _ *fixture) {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			writeControl(t, filepath.Join(f.root, "cgroup.events"), "populated 1\n")
			writeControl(t, filepath.Join(f.root, "manager", "cgroup.procs"), "42\n")
			configure(t, f)
			group := f.open(t)
			require.Error(t, drain(t.Context(), group, time.Millisecond, time.Millisecond))
			assert.FileExists(t, f.receipt)
			assert.DirExists(t, f.root)
		})
	}
}

func TestEmptyAndGracefulRemoval(t *testing.T) {
	t.Parallel()
	for _, graceful := range []bool{false, true} {
		t.Run(strconv.FormatBool(graceful), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			if graceful {
				writeControl(t, filepath.Join(f.root, "cgroup.events"), "populated 1\n")
				writeControl(t, filepath.Join(f.root, "manager", "cgroup.procs"), "42\n")
				f.env.signalPID = func(int) error {
					writeControl(t, filepath.Join(f.root, "cgroup.events"), "populated 0\n")
					return nil
				}
			}
			group := f.open(t)
			require.NoError(t, drain(t.Context(), group, time.Second, time.Second))
			assert.NoDirExists(t, f.root)
			assert.NoFileExists(t, f.receipt)
		})
	}
}

func TestRegistrationAndOpenFailures(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uint32(os.Getuid())
	require.Error(t, f.env.register(unownedRoot, fixtureRun, fixtureAttempt, uid))
	oldRoot := f.root
	f.root = filepath.Join(f.env.cgroups, "microfat-ci-123-2-789")
	require.Error(t, f.env.register(f.root, fixtureRun, fixtureAttempt, uid))
	f.root = oldRoot
	require.NoError(t, os.Remove(f.receipt))
	require.NoError(t, os.Remove(f.env.receipts))
	writeControl(t, f.env.receipts, "not a directory")
	require.Error(t, f.env.register(f.root, fixtureRun, fixtureAttempt, uid))
	require.NoError(t, os.Remove(f.env.receipts))
	f.env.receipts = filepath.Join(t.TempDir(), "missing", "registry")
	require.Error(t, f.env.register(f.root, fixtureRun, fixtureAttempt, uid))
	require.NoError(t, os.Rename(f.env.cgroups, f.env.cgroups+"other"))
	require.Error(t, f.env.register(f.root, fixtureRun, fixtureAttempt, uid))

	f = newFixture(t)
	require.NoError(t, os.Rename(f.env.cgroups, f.env.cgroups+"other"))
	_, err := f.env.open(f.root, fixtureRun, fixtureAttempt)
	require.Error(t, err)
	require.Error(t, f.env.cleanup(t.Context(), f.root, fixtureRun, fixtureAttempt))
}

func TestRevalidationAndRemovalFailures(t *testing.T) {
	t.Parallel()
	failure := errors.New("injected failure")
	for name, exercise := range map[string]func(*testing.T, *fixture, *tree){
		"pid membership vanished": func(t *testing.T, f *fixture, group *tree) {
			writeControl(t, filepath.Join(f.root, "manager", "cgroup.procs"), "42")
			f.env.openPID = func(int) (int, error) {
				require.NoError(t, os.Remove(filepath.Join(f.root, "manager", "cgroup.procs")))
				return unix.Open("/dev/null", unix.O_RDONLY, 0)
			}
			group.env = f.env
			require.Error(t, group.terminate())
		},
		"new unowned leaf before force": func(t *testing.T, f *fixture, group *tree) {
			f.group(t, filepath.Join(f.root, "unowned"))
			require.Error(t, group.force())
		},
		"vanished leaf": func(t *testing.T, _ *fixture, group *tree) {
			require.NoError(t, group.child(group.root, "microfat-bench-999", func(*os.File) error { return failure }, true))
		},
		"foreign leaf owner": func(t *testing.T, _ *fixture, group *tree) {
			group.receipt.UID++
			group.env.privilegedUID += 2
			require.Error(t, group.child(group.root, "manager", func(*os.File) error { return nil }, false))
		},
		"child filesystem error": func(t *testing.T, _ *fixture, group *tree) {
			group.env.statfs = func(int) (int64, error) { return 0, failure }
			require.ErrorIs(t, group.child(group.root, "manager", func(*os.File) error { return nil }, false), failure)
		},
		"closed walker": func(t *testing.T, _ *fixture, group *tree) {
			require.NoError(t, group.root.Close())
			require.Error(t, group.walk(func(*os.File) error { return nil }, false))
		},
		"cancelled tree": func(t *testing.T, _ *fixture, group *tree) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			group.ctx = ctx
			require.ErrorIs(t, group.walk(func(*os.File) error { return nil }, false), context.Canceled)
			require.ErrorIs(t, group.signal(group.root, 42), context.Canceled)
		},
		"cancelled directory walk": func(t *testing.T, _ *fixture, group *tree) {
			ctx, cancel := context.WithCancel(t.Context())
			group.ctx = ctx
			require.ErrorIs(t, group.walk(func(*os.File) error { cancel(); return nil }, false), context.Canceled)
		},
		"renamed root": func(t *testing.T, f *fixture, group *tree) {
			require.NoError(t, os.Rename(f.root, f.root+"other"))
			require.Error(t, group.remove())
		},
		"replaced root": func(t *testing.T, f *fixture, group *tree) {
			require.NoError(t, os.Rename(f.root, f.root+"other"))
			f.group(t, f.root)
			require.ErrorContains(t, group.remove(), "changed before removal")
			assert.FileExists(t, filepath.Join(f.root, "cgroup.events"))
		},
		"root removal denied": func(t *testing.T, _ *fixture, group *tree) {
			group.env.unlinkAt = func(fd int, name string, flags int) error {
				if name == group.name {
					return failure
				}
				return virtualUnlink(fd, name, flags)
			}
			require.ErrorIs(t, group.remove(), failure)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			group := f.open(t)
			exercise(t, f, group)
			assert.FileExists(t, f.receipt)
		})
	}
	f := newFixture(t)
	require.NoError(t, f.env.cleanup(t.Context(), f.root, fixtureRun, fixtureAttempt))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, f.env.cleanup(ctx, f.root, fixtureRun, fixtureAttempt), context.Canceled)
}

func TestBoundedTreeWalk(t *testing.T) {
	t.Parallel()
	for _, directories := range []bool{true, false} {
		t.Run(strconv.FormatBool(directories), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			group := f.open(t)
			const entries = maxGroups * 2
			for i := 0; i < entries; i++ {
				name := filepath.Join(f.root, "microfat-bench-"+strconv.Itoa(i+2))
				if directories {
					if i > maxGroups {
						break
					}
					f.group(t, name)
				} else {
					writeControl(t, name, "")
				}
			}
			require.ErrorContains(t, group.walk(func(*os.File) error { return nil }, false), "bounded")
		})
	}
}

func TestConcurrentOwnerLeafRemoval(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"during walk", "before removal", "at unlink", "replacement", "missing live control"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			group := f.open(t)
			leaf := filepath.Join(f.root, "microfat-bench-1")
			visit := func(file *os.File) error {
				if mode == "missing live control" {
					require.NoError(t, os.Remove(filepath.Join(leaf, "cgroup.procs")))
					_, err := pids(file)
					return err
				}
				if mode != "at unlink" {
					require.NoError(t, virtualUnlink(int(group.root.Fd()), filepath.Base(leaf), unix.AT_REMOVEDIR))
				}
				if mode == "replacement" {
					f.group(t, leaf)
				}
				if mode == "during walk" {
					return unix.ENOENT
				}
				return nil
			}
			if mode == "at unlink" {
				group.env.unlinkAt = func(fd int, name string, flags int) error {
					require.NoError(t, virtualUnlink(fd, name, flags))
					return unix.ENOENT
				}
			}
			err := group.child(group.root, filepath.Base(leaf), visit, true)
			if mode == "replacement" || mode == "missing live control" {
				require.Error(t, err)
				assert.DirExists(t, leaf)
			} else {
				require.NoError(t, err)
				assert.NoDirExists(t, leaf)
			}
			assert.FileExists(t, f.receipt)
		})
	}
	f := newFixture(t)
	group := f.open(t)
	require.NoError(t, os.Remove(filepath.Join(f.root, "manager", "cgroup.type")))
	require.Error(t, group.child(group.root, "manager", func(*os.File) error { return nil }, false))
}

func TestEntrypointRefusals(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"unknown"}, {"register", unownedRoot, "1", "1", "bad"},
		{"register", unownedRoot, "1", "1", "-1"}, {"register", unownedRoot, "1", "1", "4294967296"},
		{"register", unownedRoot, "1", "1", "0"}, {"cleanup", unownedRoot, "1", "1"}} {
		require.Error(t, Run(t.Context(), args))
	}
	_, err := nativeEnvironment().selfCgroup()
	require.NoError(t, err)
	_, err = nativeEnvironment().openPID(-1)
	require.Error(t, err)
	require.Error(t, nativeEnvironment().signalPID(-1))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, Cleanup(ctx, unownedRoot, "1", "1"))
}
