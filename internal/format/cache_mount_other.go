//go:build !linux

package format

func checkCacheDirExecMount(_ int, _ string) error {
	return nil
}
