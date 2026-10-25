//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

// newExtractHasherFunc constructs the streaming SHA-256 hasher for extraction paths that own
// their own in-flight digest check (such as sealed memfd execution). Cold cache materialization
// delegates in-flight temporary-file hashing to internal/cache so decompressed bytes are hashed
// once during extraction and once on the reopened read-only descriptor after publication.
var newExtractHasherFunc func() hash.Hash = sha256.New

// decompressVariantToWriter validates optional shared-dictionary metadata and checksums, checks
// variant bounds and checksum format, and streams the decompressed payload into w without wrapping
// w in an extra SHA-256 pass. Callers MUST either wrap w in a streaming SHA-256 check (memfd via
// extractVariantToWriter) or delegate to cache.MaterializeAndOpenVariantAtFDWithOpener (cold cache).
func decompressVariantToWriter(selfFile *os.File, entry *format.VariantEntry, idx *format.Index, w io.Writer) error {
	var dictBytes []byte
	if idx != nil && idx.DictionarySize > 0 {
		if idx.DictionarySize > format.MaxDictionarySize || idx.DictionaryOffset < 0 {
			return fmt.Errorf("%w: dictionary size %d or offset %d out of bounds",
				format.ErrInvalidDictionary, idx.DictionarySize, idx.DictionaryOffset)
		}
		if idx.DictionarySHA256 == "" || !format.ValidateChecksum(idx.DictionarySHA256) {
			return fmt.Errorf("%w: dictionary missing or invalid sha256 checksum", format.ErrInvalidChecksum)
		}
		dictBytes = make([]byte, idx.DictionarySize)
		if _, err := selfFile.ReadAt(dictBytes, idx.DictionaryOffset); err != nil {
			return fmt.Errorf("reading shared dictionary: %w", err)
		}
		h := sha256.Sum256(dictBytes)
		var dictHex [sha256HexDigestLen]byte
		hex.Encode(dictHex[:], h[:])
		if string(dictHex[:]) != idx.DictionarySHA256 {
			return fmt.Errorf("%w: expected %s, got %s", format.ErrDictionaryCorrupted, idx.DictionarySHA256, string(dictHex[:]))
		}
	}

	if entry.UncompressedSize <= 0 || entry.UncompressedSize > format.MaxPayloadSize {
		return fmt.Errorf("%w: variant %s uncompressed size %d invalid", format.ErrPayloadTooLarge, entry.Level, entry.UncompressedSize)
	}
	if entry.SHA256 == "" || !format.ValidateChecksum(entry.SHA256) {
		return fmt.Errorf("%w: variant %s missing or invalid sha256 checksum", format.ErrInvalidChecksum, entry.Level)
	}

	c, err := codec.Get(entry.Compression)
	if err != nil {
		return fmt.Errorf("lookup codec %q for variant %s: %w", entry.Compression, entry.Level, err)
	}

	secReader := io.NewSectionReader(selfFile, entry.Offset, entry.CompressedSize)
	if err := codec.DecompressWithOptionalDict(c, w, secReader, entry.UncompressedSize, dictBytes); err != nil {
		return fmt.Errorf("decompressing variant payload: %w", err)
	}

	return nil
}

// extractVariantToWriter seeks to the variant offset and streams decompressed bytes to w,
// verifying the payload SHA-256 digest concurrently during decompression.
func extractVariantToWriter(selfFile *os.File, entry *format.VariantEntry, idx *format.Index, w io.Writer) error {
	hasher := newExtractHasherFunc()
	mw := io.MultiWriter(w, hasher)
	if err := decompressVariantToWriter(selfFile, entry, idx, mw); err != nil {
		return err
	}

	var sum [sha256.Size]byte
	var hexBuf [sha256HexDigestLen]byte
	hex.Encode(hexBuf[:], hasher.Sum(sum[:0]))
	if string(hexBuf[:]) != entry.SHA256 {
		return fmt.Errorf("%w: expected %s, got %s", format.ErrPayloadCorrupted, entry.SHA256, string(hexBuf[:]))
	}

	return nil
}
