// Userns runner is an explicitly privileged test-only bootstrap. Go writes the
// exact UID/GID maps directly, without changing host subordinate-ID policy.
package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

const (
	minimumArgs      = 4
	commandIndex     = 3
	bootstrapTimeout = 45 * time.Second
	drainTimeout     = 2 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "userns bootstrap:", err)
		os.Exit(1)
	}
}

func run() error {
	if os.Geteuid() != 0 || len(os.Args) < minimumArgs {
		return errors.New("requires explicit privileged setup: userns-runner uid gid command [args...]")
	}
	uid, uidErr := strconv.Atoi(os.Args[1])
	gid, gidErr := strconv.Atoi(os.Args[2])
	if uidErr != nil || gidErr != nil || uid <= 0 || gid <= 0 || uint64(uid) >= math.MaxUint32 || uint64(gid) >= math.MaxUint32 {
		return errors.New("ordinary mapped UID/GID required")
	}
	groups, err := os.Getgroups()
	if err != nil || len(groups) != 0 {
		return errors.New("bootstrap requires cleared supplementary groups")
	}
	// Pdeathsig tracks the creating thread. Keep it alive until unshare exits;
	// unshare --kill-child then tears down its PID-1 supervisor and descendants.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, bootstrapTimeout)
	defer cancel()
	// #nosec G204 G702 -- explicit test-only privileged bootstrap, supplied by the E2E controller.
	cmd := exec.CommandContext(ctx, os.Args[commandIndex], os.Args[minimumArgs:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.WaitDelay = drainTimeout
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: 1}, {ContainerID: uid, HostID: uid, Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: 1}, {ContainerID: gid, HostID: gid, Size: 1}},
		Pdeathsig:   syscall.SIGKILL,
	}
	return errors.Join(cmd.Run(), ctx.Err())
}
