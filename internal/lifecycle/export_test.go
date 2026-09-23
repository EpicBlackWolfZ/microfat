package lifecycle

import "os"

// SetReadXattrsFuncForTest sets readXattrsFunc for unit testing.
func SetReadXattrsFuncForTest(fn func(string) (map[string][]byte, error)) func() {
	orig := readXattrsFunc
	readXattrsFunc = fn
	return func() { readXattrsFunc = orig }
}

// SetSetFdXattrFuncForTest sets setFdXattrFunc for unit testing.
func SetSetFdXattrFuncForTest(fn func(int, string, []byte) error) func() {
	orig := setFdXattrFunc
	setFdXattrFunc = fn
	return func() { setFdXattrFunc = orig }
}

// SetChownFuncForTest sets chownFunc for unit testing.
func SetChownFuncForTest(fn func(*os.File, int, int) error) func() {
	orig := chownFunc
	chownFunc = fn
	return func() { chownFunc = orig }
}

// SetChmodFuncForTest sets chmodFunc for unit testing.
func SetChmodFuncForTest(fn func(*os.File, os.FileMode) error) func() {
	orig := chmodFunc
	chmodFunc = fn
	return func() { chmodFunc = orig }
}

// SetSyncDirFuncForTest sets syncDirFunc for unit testing.
func SetSyncDirFuncForTest(fn func(string) error) func() {
	orig := syncDirFunc
	syncDirFunc = fn
	return func() { syncDirFunc = orig }
}

// SetPublishCreateOnlyFuncForTest sets publishCreateOnlyFunc for unit testing.
func SetPublishCreateOnlyFuncForTest(fn func(string, string) error) func() {
	orig := publishCreateOnlyFunc
	publishCreateOnlyFunc = fn
	return func() { publishCreateOnlyFunc = orig }
}

// SetAcquireLockFuncForTest sets acquireLockFunc for unit testing.
func SetAcquireLockFuncForTest(fn func(string) (func(), error)) func() {
	orig := acquireLockFunc
	acquireLockFunc = fn
	return func() { acquireLockFunc = orig }
}

// SetFileStatMetadataFuncForTest sets fileStatMetadataFunc for unit testing.
func SetFileStatMetadataFuncForTest(fn func(os.FileInfo) (uint64, uint64, uint64, int, int, bool)) func() {
	orig := fileStatMetadataFunc
	fileStatMetadataFunc = fn
	return func() { fileStatMetadataFunc = orig }
}

// SetRenameFuncForTest sets renameFunc for unit testing.
func SetRenameFuncForTest(fn func(string, string) error) func() {
	orig := renameFunc
	renameFunc = fn
	return func() { renameFunc = orig }
}

// SetGeteuidFuncForTest sets geteuidFunc for unit testing.
func SetGeteuidFuncForTest(fn func() int) func() {
	orig := geteuidFunc
	geteuidFunc = fn
	return func() { geteuidFunc = orig }
}

// FileStatMetadataForTest exports fileStatMetadata for unit testing.
func FileStatMetadataForTest(fi os.FileInfo) (uint64, uint64, uint64, int, int, bool) {
	return fileStatMetadata(fi)
}

// ReadXattrsForTest exports readXattrs for unit testing.
func ReadXattrsForTest(path string) (map[string][]byte, error) {
	return readXattrs(path)
}

// PublishCreateOnlyForTest exports publishCreateOnly for unit testing.
func PublishCreateOnlyForTest(stagedPath, destPath string) error {
	return publishCreateOnly(stagedPath, destPath)
}

// AcquireAdvisoryLockForTest exports acquireAdvisoryLock for unit testing.
func AcquireAdvisoryLockForTest(path string) (func(), error) {
	return acquireAdvisoryLock(path)
}

// SyncDirectoryForTest exports syncDirectory for unit testing.
func SyncDirectoryForTest(dir string) error {
	return syncDirectory(dir)
}
