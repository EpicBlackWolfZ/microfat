//go:build linux

package lifecycle

// SetRenameat2FuncForTest sets renameat2Func for unit testing on Linux.
func SetRenameat2FuncForTest(fn func(int, string, int, string, uint) error) func() {
	orig := renameat2Func
	renameat2Func = fn
	return func() { renameat2Func = orig }
}

// SetFlockFuncForTest sets flockFunc for unit testing on Linux.
func SetFlockFuncForTest(fn func(int, int) error) func() {
	orig := flockFunc
	flockFunc = fn
	return func() { flockFunc = orig }
}

// SetListxattrFuncForTest sets listxattrFunc for unit testing on Linux.
func SetListxattrFuncForTest(fn func(string, []byte) (int, error)) func() {
	orig := listxattrFunc
	listxattrFunc = fn
	return func() { listxattrFunc = orig }
}

// SetGetxattrFuncForTest sets getxattrFunc for unit testing on Linux.
func SetGetxattrFuncForTest(fn func(string, string, []byte) (int, error)) func() {
	orig := getxattrFunc
	getxattrFunc = fn
	return func() { getxattrFunc = orig }
}
