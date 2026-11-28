package cache

import "errors"

// Common cache validation errors.
var (
	// ErrNonRegularFile indicates that the cache entry is not a standard regular file (e.g. symlink or directory).
	ErrNonRegularFile = errors.New("cached path is not a regular file")

	// ErrUnsafeFile indicates an unexpected owner, writable group/other mode, or setuid/setgid/sticky bits.
	ErrUnsafeFile = errors.New("cache file has unexpected owner or unsafe permissions")

	// ErrNotExecutable marks a safe regular entry that lacks owner execute permission.
	ErrNotExecutable = errors.New("cache file lacks owner execute permission")

	// ErrSizeMismatch indicates that the cache file length does not match the expected uncompressed variant size.
	ErrSizeMismatch = errors.New("cache file size mismatch")
)

/*@
ghost
decreases
pure func safeCacheOwnerAndMode(uid uint32, unsafeMode bool, expectedUID int) bool {
	return expectedUID >= 0 && int64(uid) == int64(expectedUID) && !unsafeMode
}
@*/

// checkCacheMetadataFields validates ownership and safe permission bits (0o7022 unset) for cache entries.
// @ requires ErrUnsafeFile != nil && isComparable(ErrUnsafeFile)
// @ ensures (err == nil) == safeCacheOwnerAndMode(uid, unsafeMode, expectedUID)
// @ ensures err != nil ==> isComparable(err) && err == ErrUnsafeFile
// @ decreases
func checkCacheMetadataFields(uid uint32, unsafeMode bool, expectedUID int) /*@ (err @*/ error /*@ ) @*/ {
	if expectedUID < 0 || int64(uid) != int64(expectedUID) || unsafeMode {
		return ErrUnsafeFile
	}
	return nil
}

// checkCacheEntryFields validates regular-file type, ownership/mode safety, optional owner execute bit,
// and optional exact uncompressed byte length for descriptor-bound cache lookups.
// @ requires ErrNonRegularFile != nil && isComparable(ErrNonRegularFile)
// @ requires ErrUnsafeFile != nil && isComparable(ErrUnsafeFile)
// @ requires ErrNotExecutable != nil && isComparable(ErrNotExecutable)
// @ requires ErrSizeMismatch != nil && isComparable(ErrSizeMismatch)
// @ ensures !isRegular ==> err == ErrNonRegularFile
// @ ensures isRegular && !safeCacheOwnerAndMode(uid, unsafeMode, expectedUID) ==> err == ErrUnsafeFile
// @ ensures err == nil ==> isRegular && safeCacheOwnerAndMode(uid, unsafeMode, expectedUID)
// @ ensures err == nil && requireExec ==> hasOwnerExec
// @ ensures err == nil && checkSize ==> size == expectedSize
// @ decreases
func checkCacheEntryFields(
	uid uint32,
	isRegular, unsafeMode, hasOwnerExec bool,
	size int64,
	expectedUID int,
	expectedSize int64,
	requireExec, checkSize bool,
) /*@ (err @*/ error /*@ ) @*/ {
	if !isRegular {
		return ErrNonRegularFile
	}
	if err := checkCacheMetadataFields(uid, unsafeMode, expectedUID); err != nil {
		return err
	}
	if requireExec && !hasOwnerExec {
		return ErrNotExecutable
	}
	if checkSize && size != expectedSize {
		return ErrSizeMismatch
	}
	return nil
}
