package cache

// Hash observers allow tests to count streaming materialization and descriptor
// verification passes without replacing the concrete SHA-256 implementation.
// Keeping SHA-256 construction at each call site lets the compiler retain scratch
// buffers on the stack. Observers are nil during normal operation.
var (
	StreamHashStartedFunc func()
	VerifyHashStartedFunc func()
)
