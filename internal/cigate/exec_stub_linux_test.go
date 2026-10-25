//go:build linux

package cigate

import (
	"bytes"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

const executableStubMode = 0o755

// execScriptLock prevents the Linux ETXTBSY ("Text file busy") race across parallel
// tests in this package: when one goroutine writes an executable stub (holding a brief
// O_WRONLY descriptor) while another goroutine invokes cmd.Start(), ForkExec's child
// transiently inherits that write descriptor until its own execve closes O_CLOEXEC FDs.
// Holding Lock across stub creation and RLock only across cmd.Start() (releasing before
// cmd.Wait()) guarantees no un-exec'd child holds a write reference to any stub inode
// while keeping subprocess execution parallel.
var execScriptLock sync.RWMutex

func writeExecutableTestStub(t *testing.T, path, content string) {
	t.Helper()
	execScriptLock.Lock()
	err := os.WriteFile(path, []byte(content), executableStubMode)
	execScriptLock.Unlock()
	require.NoError(t, err)
}

func runCommandSafe(cmd *exec.Cmd) error {
	execScriptLock.RLock()
	err := cmd.Start()
	execScriptLock.RUnlock()
	if err != nil {
		return err
	}
	return cmd.Wait()
}

func combinedOutputSafe(cmd *exec.Cmd) ([]byte, error) {
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := runCommandSafe(cmd)
	return buf.Bytes(), err
}
