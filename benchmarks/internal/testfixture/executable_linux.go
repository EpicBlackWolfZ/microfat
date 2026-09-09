//go:build linux

package testfixture

import (
	"os"
	"syscall"
)

// WriteExecutable prevents another test's fork from inheriting a writable script
// descriptor. CLOEXEC alone closes it only at exec, leaving an ETXTBSY window.
func WriteExecutable(path string, contents []byte) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, contents, 0o755) // #nosec G306 -- executable fixture inside a private test directory.
}
