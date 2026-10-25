package cache

import (
	"crypto/sha256"
	"hash"
)

// Hasher constructors used by streaming temporary-file materialization and descriptor-bound
// post-publication / cache-hit verification. Tests override these to assert exact hash pass counts.
var (
	NewStreamHasherFunc func() hash.Hash = sha256.New
	NewVerifyHasherFunc func() hash.Hash = sha256.New
)
