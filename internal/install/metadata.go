package install

import "errors"

// MaxFileBytes bounds the maximum size of a single installed generation binary (250 MiB).
const MaxFileBytes int64 = 250 * 1024 * 1024

// ErrOwnership indicates an ownership, permission, symlink, or link-count trust boundary failure.
var ErrOwnership = errors.New("installation ownership validation failed")

/*@
ghost
decreases
pure func installUIDAllowed(uid, expectedUID int64, exactUID bool) bool {
	return expectedUID >= 0 && ((exactUID && uid == expectedUID) || (!exactUID && (uid == 0 || uid == expectedUID)))
}

ghost
decreases
pure func installEntrySafe(
	uid, expectedUID int64,
	isDir, isRegular bool,
	nlink uint64,
	hasUnsafeWriteBits, isSticky, hasSetuidOrSetgid, expectDir, exactUID bool,
) bool {
	return installUIDAllowed(uid, expectedUID, exactUID) &&
		(expectDir ?
			(isDir && ((!exactUID && uid == 0 && isSticky) || (!hasUnsafeWriteBits && !hasSetuidOrSetgid))) :
			(isRegular && nlink == 1 && !hasUnsafeWriteBits && !hasSetuidOrSetgid))
}
@*/

// checkInstallEntryMetadata verifies UID ownership, type (directory vs non-hard-linked regular file),
// optional root-sticky ancestor allowance, and absence of group/world write or setuid/setgid bits.
/*@
requires ErrOwnership != nil && isComparable(ErrOwnership)
ensures (err == nil) == installEntrySafe(
	uid, expectedUID, isDir, isRegular, nlink,
	hasUnsafeWriteBits, isSticky, hasSetuidOrSetgid, expectDir, exactUID,
)
ensures err != nil ==> isComparable(err) && err == ErrOwnership
decreases
@*/
func checkInstallEntryMetadata(
	uid, expectedUID int64,
	isDir, isRegular bool,
	nlink uint64,
	hasUnsafeWriteBits, isSticky, hasSetuidOrSetgid, expectDir, exactUID bool,
) /*@ (err @*/ error /*@ ) @*/ {
	if expectedUID < 0 {
		return ErrOwnership
	}
	if (exactUID && uid != expectedUID) || (!exactUID && uid != 0 && uid != expectedUID) {
		return ErrOwnership
	}
	if expectDir {
		if !isDir {
			return ErrOwnership
		}
		if !exactUID && uid == 0 && isSticky {
			return nil
		}
	} else if !isRegular || nlink != 1 {
		return ErrOwnership
	}
	if hasUnsafeWriteBits || hasSetuidOrSetgid {
		return ErrOwnership
	}
	return nil
}

// checkInstallFileSize validates that a generation product file size is strictly positive and <= MaxFileBytes.
// @ ensures ok == (0 < size && size <= MaxFileBytes)
// @ decreases
func checkInstallFileSize(size int64) /*@ (ok @*/ bool /*@ ) @*/ {
	return size > 0 && size <= MaxFileBytes
}
