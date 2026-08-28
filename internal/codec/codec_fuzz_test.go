package codec

import (
	"bytes"
	"testing"
)

func FuzzDecompressZstd(f *testing.F) {
	zCodec := NewZstdCodec()

	// Seed 1: Valid compressed stream
	var buf bytes.Buffer
	src := []byte("Hello, microfat high-performance multi-architecture Go binary loader!")
	if err := zCodec.Compress(&buf, src, "default"); err == nil {
		f.Add(buf.Bytes(), int64(len(src)))
	}

	// Seed 2: Empty stream
	var emptyBuf bytes.Buffer
	if err := zCodec.Compress(&emptyBuf, []byte{}, "default"); err == nil {
		f.Add(emptyBuf.Bytes(), int64(0))
	}

	// Seed 3: Corrupt inputs
	f.Add([]byte("\x28\xb5\x2f\xfd\x00\x00"), int64(100))
	f.Add([]byte("not a zstd frame"), int64(50))
	f.Add([]byte{}, int64(0))

	f.Fuzz(func(t *testing.T, data []byte, uncompressedSize int64) {
		if uncompressedSize < 0 || uncompressedSize > 10*1024*1024 {
			// Bound to prevent fuzzer allocating out of memory on huge uncompressedSize parameters
			return
		}
		var out bytes.Buffer
		_ = zCodec.Decompress(&out, bytes.NewReader(data), uncompressedSize)
	})
}

func FuzzDecompressLZ4(f *testing.F) {
	lzCodec := NewLZ4Codec()

	// Seed 1: Valid compressed stream
	var buf bytes.Buffer
	src := []byte("Microfat fast lz4 frame compression payload test stream.")
	if err := lzCodec.Compress(&buf, src, "default"); err == nil {
		f.Add(buf.Bytes(), int64(len(src)))
	}

	// Seed 2: Corrupt inputs
	f.Add([]byte("\x04\x22\x4d\x18"), int64(10))
	f.Add([]byte("random bytes"), int64(20))

	f.Fuzz(func(t *testing.T, data []byte, uncompressedSize int64) {
		if uncompressedSize < 0 || uncompressedSize > 10*1024*1024 {
			return
		}
		var out bytes.Buffer
		_ = lzCodec.Decompress(&out, bytes.NewReader(data), uncompressedSize)
	})
}

func FuzzDecompressZstdDict(f *testing.F) {
	zCodec := NewZstdCodec()
	dict := []byte("shared-dictionary-prefix-symbols-and-elf-constants-for-microfat")
	src := []byte("shared-dictionary-prefix-symbols-and-elf-constants-for-microfat payload content variant v3")

	var buf bytes.Buffer
	if err := zCodec.CompressWithDict(&buf, src, "default", dict); err == nil {
		f.Add(buf.Bytes(), dict, int64(len(src)))
	}

	f.Add([]byte("corrupt-stream"), dict, int64(len(src)))
	f.Add(buf.Bytes(), []byte("wrong-dict"), int64(len(src)))

	f.Fuzz(func(t *testing.T, stream []byte, dictionary []byte, uncompressedSize int64) {
		if uncompressedSize < 0 || uncompressedSize > 10*1024*1024 {
			return
		}
		var out bytes.Buffer
		_ = zCodec.DecompressWithDict(&out, bytes.NewReader(stream), uncompressedSize, dictionary)
	})
}

func FuzzTrainDictionary(f *testing.F) {
	s1 := []byte("common elf header prefix bytes variant 1 payload symbol table strings")
	s2 := []byte("common elf header prefix bytes variant 2 payload symbol table strings")
	s3 := []byte("common elf header prefix bytes variant 3 payload symbol table strings")

	f.Add(s1, s2, s3, 1024)

	f.Fuzz(func(t *testing.T, sample1, sample2, sample3 []byte, dictSize int) {
		samples := [][]byte{sample1, sample2, sample3}
		dict, err := TrainDictionary(samples, dictSize, "default")
		if err == nil && len(dict) > 0 {
			zCodec := NewZstdCodec()
			var compBuf bytes.Buffer
			cErr := zCodec.CompressWithDict(&compBuf, sample1, "default", dict)
			if cErr == nil {
				var decompBuf bytes.Buffer
				dErr := zCodec.DecompressWithDict(&decompBuf, bytes.NewReader(compBuf.Bytes()), int64(len(sample1)), dict)
				if dErr == nil && !bytes.Equal(decompBuf.Bytes(), sample1) {
					t.Fatalf("decompressed payload mismatch with trained dictionary")
				}
			}
		}
	})
}

func FuzzDecompressNone(f *testing.F) {
	nCodec := NewNoneCodec()
	src := []byte("uncompressed raw binary payload")
	f.Add(src, int64(len(src)))

	f.Fuzz(func(t *testing.T, data []byte, uncompressedSize int64) {
		if uncompressedSize < 0 || uncompressedSize > int64(len(data)) {
			var out bytes.Buffer
			_ = nCodec.Decompress(&out, bytes.NewReader(data), uncompressedSize)
			return
		}
		var out bytes.Buffer
		err := nCodec.Decompress(&out, bytes.NewReader(data), uncompressedSize)
		if uncompressedSize == int64(len(data)) && err != nil {
			t.Fatalf("expected no error when uncompressedSize matches data length, got: %v", err)
		}
	})
}
