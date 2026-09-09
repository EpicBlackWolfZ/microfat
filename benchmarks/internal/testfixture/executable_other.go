//go:build !linux

package testfixture

import "os"

func WriteExecutable(path string, contents []byte) error {
	return os.WriteFile(path, contents, 0o755) // #nosec G306 -- executable fixture inside a private test directory.
}
