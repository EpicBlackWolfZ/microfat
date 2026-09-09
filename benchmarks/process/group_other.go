//go:build !unix

package process

import (
	"os"
	"os/exec"
)

func configureGroup(_ *exec.Cmd)                    {}
func signalGroup(process *os.Process, _ bool) error { return process.Kill() }
