//go:build unix

package process

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func signalGroup(process *os.Process, force bool) error {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	err := syscall.Kill(-process.Pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
