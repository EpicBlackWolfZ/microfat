package codec_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
)

const (
	testPayloadSizeSmall = 128 * 1024  // 128 KB
	testPayloadSizeLarge = 1024 * 1024 // 1 MB
	testPayloadSizeTiny  = 64 * 1024   // 64 KB
	testCodecZstd        = "zstd"
	testLevelFastest     = "fastest"
)

func generateTestData(size int) []byte {
	buf := make([]byte, size)
	// Fill with semi-compressible pattern
	for i := range buf {
		buf[i] = byte((i % 256) ^ (i / 1024))
	}
	return buf
}

func TestRegistry(t *testing.T) {
	t.Parallel()

	list := codec.List()
	if len(list) < 3 {
		t.Fatalf("expected at least 3 codecs, got %d: %v", len(list), list)
	}

	for _, name := range []string{testCodecZstd, "lz4", "none", "ZSTD", "Lz4", "NoNe", ""} {
		c, err := codec.Get(name)
		if err != nil {
			t.Fatalf("unexpected error getting codec %q: %v", name, err)
		}
		if c == nil {
			t.Fatalf("expected non-nil codec for %q", name)
		}
	}

	_, err := codec.Get("invalid_codec_name")
	if !errors.Is(err, codec.ErrUnsupportedCodec) {
		t.Fatalf("expected ErrUnsupportedCodec, got: %v", err)
	}

	// Register nil is safe
	codec.Register(nil)
}

func TestParseCompressionSpec(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input     string
		wantAlgo  string
		wantLevel string
	}{
		{"", "", ""},
		{testCodecZstd, testCodecZstd, ""},
		{"zstd:best", testCodecZstd, "best"},
		{"lz4:fastest", "lz4", testLevelFastest},
		{"ZSTD:11", testCodecZstd, "11"},
		{"none", "none", ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			algo, level := codec.ParseCompressionSpec(tc.input)
			if algo != tc.wantAlgo || level != tc.wantLevel {
				t.Fatalf("ParseCompressionSpec(%q) = (%q, %q), want (%q, %q)",
					tc.input, algo, level, tc.wantAlgo, tc.wantLevel)
			}
		})
	}
}

func TestResolveCompression(t *testing.T) {
	t.Parallel()

	t.Run("Default balanced", func(t *testing.T) {
		t.Parallel()
		c, level, err := codec.ResolveCompression("", "", "", 1024)
		if err != nil || c.Name() != codec.AlgorithmZstd || level != "better" {
			t.Fatalf("unexpected resolve: c=%v, level=%s, err=%v", c, level, err)
		}
	})

	t.Run("Profile size", func(t *testing.T) {
		t.Parallel()
		c, level, err := codec.ResolveCompression(codec.ProfileSize, "", "", 1024)
		if err != nil || c.Name() != codec.AlgorithmZstd || level != "best" {
			t.Fatalf("unexpected resolve: c=%v, level=%s, err=%v", c, level, err)
		}
	})

	t.Run("Profile latency tiny payload auto-promotes to none", func(t *testing.T) {
		t.Parallel()
		c, level, err := codec.ResolveCompression(codec.ProfileLatency, "", "", testPayloadSizeTiny)
		if err != nil || c.Name() != codec.AlgorithmNone {
			t.Fatalf("unexpected resolve: c=%v, level=%s, err=%v", c, level, err)
		}
	})

	t.Run("Profile latency large payload defaults to lz4", func(t *testing.T) {
		t.Parallel()
		c, _, err := codec.ResolveCompression(codec.ProfileLatency, "", "", testPayloadSizeLarge)
		if err != nil || c.Name() != codec.AlgorithmLZ4 {
			t.Fatalf("unexpected resolve: c=%v, err=%v", c, err)
		}
	})

	t.Run("Profile latency with explicit lz4 does not auto-promote", func(t *testing.T) {
		t.Parallel()
		c, _, err := codec.ResolveCompression(codec.ProfileLatency, "lz4", "", testPayloadSizeTiny)
		if err != nil || c.Name() != codec.AlgorithmLZ4 {
			t.Fatalf("unexpected resolve: c=%v, err=%v", c, err)
		}
	})

	t.Run("Explicit algorithm and spec level override", func(t *testing.T) {
		t.Parallel()
		c, level, err := codec.ResolveCompression(codec.ProfileLatency, "zstd:9", "", 1024)
		if err != nil || c.Name() != codec.AlgorithmZstd || level != "9" {
			t.Fatalf("unexpected resolve: c=%v, level=%s, err=%v", c, level, err)
		}
	})

	t.Run("Profile size with custom algo and level", func(t *testing.T) {
		t.Parallel()
		c, level, err := codec.ResolveCompression(codec.ProfileSize, testCodecZstd, "19", 1024)
		if err != nil || c.Name() != codec.AlgorithmZstd || level != "19" {
			t.Fatalf("unexpected resolve: c=%v, level=%s, err=%v", c, level, err)
		}
	})

	t.Run("Profile balanced with explicit level", func(t *testing.T) {
		t.Parallel()
		c, level, err := codec.ResolveCompression(codec.ProfileBalanced, testCodecZstd, testLevelFastest, 1024)
		if err != nil || c.Name() != codec.AlgorithmZstd || level != testLevelFastest {
			t.Fatalf("unexpected resolve: c=%v, level=%s, err=%v", c, level, err)
		}
	})

	t.Run("Default with explicit level", func(t *testing.T) {
		t.Parallel()
		c, level, err := codec.ResolveCompression("", testCodecZstd, testLevelFastest, 1024)
		if err != nil || c.Name() != codec.AlgorithmZstd || level != testLevelFastest {
			t.Fatalf("unexpected resolve: c=%v, level=%s, err=%v", c, level, err)
		}
	})

	t.Run("Invalid profile error", func(t *testing.T) {
		t.Parallel()
		_, _, err := codec.ResolveCompression("unknown_profile", "", "", 1024)
		if !errors.Is(err, codec.ErrUnsupportedProfile) {
			t.Fatalf("expected ErrUnsupportedProfile, got %v", err)
		}
	})

	t.Run("Invalid algorithm error", func(t *testing.T) {
		t.Parallel()
		_, _, err := codec.ResolveCompression("", "unknown_algo", "", 1024)
		if !errors.Is(err, codec.ErrUnsupportedCodec) {
			t.Fatalf("expected ErrUnsupportedCodec, got %v", err)
		}
	})
}

func TestCodecsRoundtrip(t *testing.T) {
	t.Parallel()

	testData := generateTestData(testPayloadSizeSmall)

	tests := []struct {
		name   string
		codec  string
		levels []string
	}{
		{
			name:   "Zstd",
			codec:  codec.AlgorithmZstd,
			levels: []string{"", "fastest", "default", "better", "best", "1", "2", "3", "5", "10", "11", "19", "0", "-1"},
		},
		{
			name:   "LZ4",
			codec:  codec.AlgorithmLZ4,
			levels: []string{"", "default", "fast", "fastest", "better", "best", "1", "2", "3", "4", "5", "6", "7", "8", "9", "0", "-1"},
		},
		{
			name:   "None",
			codec:  codec.AlgorithmNone,
			levels: []string{""},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := codec.Get(tc.codec)
			if err != nil {
				t.Fatalf("failed to get codec %s: %v", tc.codec, err)
			}

			for _, lvl := range tc.levels {
				lvl := lvl
				t.Run("level="+lvl, func(t *testing.T) {
					var compressed bytes.Buffer
					if err := c.Compress(&compressed, testData, lvl); err != nil {
						t.Fatalf("Compress failed with level %q: %v", lvl, err)
					}

					var decompressed bytes.Buffer
					if err := c.Decompress(&decompressed, &compressed, int64(len(testData))); err != nil {
						t.Fatalf("Decompress failed: %v", err)
					}

					if !bytes.Equal(decompressed.Bytes(), testData) {
						t.Fatalf("decompressed data mismatch")
					}
				})
			}
		})
	}
}

func TestCodecErrors(t *testing.T) {
	t.Parallel()

	data := generateTestData(1024)

	for _, name := range []string{codec.AlgorithmZstd, codec.AlgorithmLZ4, codec.AlgorithmNone} {
		name := name
		t.Run(name+" corrupted data", func(t *testing.T) {
			t.Parallel()
			c, err := codec.Get(name)
			if err != nil {
				t.Fatalf("Get(%q): %v", name, err)
			}

			corrupted := []byte("definitely not valid compressed stream data payload header")
			var out bytes.Buffer
			err = c.Decompress(&out, bytes.NewReader(corrupted), int64(len(data)))
			if name == codec.AlgorithmNone {
				// none will read the corrupted bytes and fail on size mismatch
				if !errors.Is(err, codec.ErrSizeMismatch) {
					t.Fatalf("expected ErrSizeMismatch, got %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error decompressing corrupted stream")
				}
			}
		})

		t.Run(name+" size mismatch", func(t *testing.T) {
			t.Parallel()
			c, _ := codec.Get(name)
			var compressed bytes.Buffer
			_ = c.Compress(&compressed, data, "")

			var out bytes.Buffer
			// Expect wrong size
			err := c.Decompress(&out, &compressed, int64(len(data)+100))
			if !errors.Is(err, codec.ErrSizeMismatch) {
				t.Fatalf("expected ErrSizeMismatch, got %v", err)
			}
		})
	}

	t.Run("Invalid level parsing", func(t *testing.T) {
		t.Parallel()
		zstdC, _ := codec.Get(codec.AlgorithmZstd)
		var buf bytes.Buffer
		err := zstdC.Compress(&buf, data, "invalid_num_abc")
		if !errors.Is(err, codec.ErrInvalidCompressionLevel) {
			t.Fatalf("expected ErrInvalidCompressionLevel, got %v", err)
		}

		lz4C, _ := codec.Get(codec.AlgorithmLZ4)
		err = lz4C.Compress(&buf, data, "invalid_num_abc")
		if !errors.Is(err, codec.ErrInvalidCompressionLevel) {
			t.Fatalf("expected ErrInvalidCompressionLevel, got %v", err)
		}
	})

	t.Run("Writer and reader errors", func(t *testing.T) {
		t.Parallel()
		ew := &errWriter{}
		er := &errReader{}

		for _, name := range []string{codec.AlgorithmZstd, codec.AlgorithmLZ4, codec.AlgorithmNone} {
			c, _ := codec.Get(name)
			if err := c.Compress(ew, data, ""); err == nil {
				t.Fatalf("expected error compressing to errWriter for %s", name)
			}
			var buf bytes.Buffer
			if err := c.Decompress(&buf, er, int64(len(data))); err == nil {
				t.Fatalf("expected error decompressing from errReader for %s", name)
			}
		}
	})

	t.Run("ParseLevelInt empty and default", func(t *testing.T) {
		t.Parallel()
		val, err := codec.ParseLevelInt("", 42)
		if err != nil || val != 42 {
			t.Fatalf("expected 42, got %d, err %v", val, err)
		}
	})
}

type errWriter struct{}

func (e *errWriter) Write(p []byte) (n int, err error) {
	return 0, errors.New("simulated write error")
}

type errReader struct{}

func (e *errReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("simulated read error")
}

func generateDictionarySamples() [][]byte {
	samples := make([][]byte, 40)
	for i := range samples {
		var b bytes.Buffer
		for j := 0; j < 50; j++ {
			b.WriteString(fmt.Sprintf("GO_MICROFAT_RUNTIME_SYMBOL_TABLE_ENTRY_PKG_INDEX_%d_%d_OFFSET_%d_LENGTH_%d\n", i, j, i*100+j, (i+j)*8))
			b.WriteString("common_static_data_string_for_testing_zstandard_compression_dictionary_builder\n")
		}
		samples[i] = b.Bytes()
	}
	return samples
}

func TestZstdDictionaryCompression(t *testing.T) {
	t.Parallel()

	samples := generateDictionarySamples()

	dict, err := codec.TrainDictionary(samples, 32*1024, "better")
	if err != nil {
		t.Fatalf("TrainDictionary failed: %v", err)
	}
	if len(dict) == 0 {
		t.Fatalf("expected non-empty dictionary")
	}

	zCodecRaw, err := codec.Get(codec.AlgorithmZstd)
	if err != nil {
		t.Fatalf("Get zstd: %v", err)
	}
	zCodec, ok := zCodecRaw.(codec.DictCodec)
	if !ok {
		t.Fatalf("expected zstd codec to implement DictCodec")
	}

	testData := samples[0]

	t.Run("Compress and Decompress with Dict", func(t *testing.T) {
		t.Parallel()
		var compressed bytes.Buffer
		if err := zCodec.CompressWithDict(&compressed, testData, "fastest", dict); err != nil {
			t.Fatalf("CompressWithDict failed: %v", err)
		}

		var decompressed bytes.Buffer
		if err := zCodec.DecompressWithDict(&decompressed, &compressed, int64(len(testData)), dict); err != nil {
			t.Fatalf("DecompressWithDict failed: %v", err)
		}

		if !bytes.Equal(decompressed.Bytes(), testData) {
			t.Fatalf("decompressed payload with dict mismatch")
		}
	})

	t.Run("DecompressWithOptionalDict helper", func(t *testing.T) {
		t.Parallel()
		var compressed bytes.Buffer
		if err := zCodec.CompressWithDict(&compressed, testData, "fastest", dict); err != nil {
			t.Fatalf("CompressWithDict failed: %v", err)
		}

		var decompressed bytes.Buffer
		if err := codec.DecompressWithOptionalDict(zCodec, &decompressed, &compressed, int64(len(testData)), dict); err != nil {
			t.Fatalf("DecompressWithOptionalDict failed: %v", err)
		}

		if !bytes.Equal(decompressed.Bytes(), testData) {
			t.Fatalf("DecompressWithOptionalDict payload mismatch")
		}

		// Also test DecompressWithOptionalDict on non-dict codec (None)
		noneCodec, _ := codec.Get(codec.AlgorithmNone)
		var noneOut bytes.Buffer
		err := codec.DecompressWithOptionalDict(noneCodec, &noneOut, bytes.NewReader(testData), int64(len(testData)), dict)
		if err != nil {
			t.Fatalf("DecompressWithOptionalDict with noneCodec: %v", err)
		}
	})

	t.Run("Decompress without matching dict fails or errs on corrupted data", func(t *testing.T) {
		t.Parallel()
		var compressed bytes.Buffer
		if err := zCodec.CompressWithDict(&compressed, testData, "fastest", dict); err != nil {
			t.Fatalf("CompressWithDict failed: %v", err)
		}

		wrongDict := []byte("invalid_or_wrong_dictionary_content_bytes_sequence_1234567890")
		var decompressed bytes.Buffer
		// Decompressing with empty dict or wrong dict should fail
		err := zCodec.DecompressWithDict(&decompressed, &compressed, int64(len(testData)), nil)
		if err == nil && !bytes.Equal(decompressed.Bytes(), testData) {
			t.Fatalf("expected error or mismatch when decompressing without dict")
		}

		var decompressedWrong bytes.Buffer
		_ = zCodec.DecompressWithDict(&decompressedWrong, bytes.NewReader(compressed.Bytes()), int64(len(testData)), wrongDict)
	})

	t.Run("Compress with invalid level", func(t *testing.T) {
		t.Parallel()
		var compressed bytes.Buffer
		err := zCodec.CompressWithDict(&compressed, testData, "invalid_num_abc", dict)
		if !errors.Is(err, codec.ErrInvalidCompressionLevel) {
			t.Fatalf("expected ErrInvalidCompressionLevel, got %v", err)
		}
	})

	t.Run("Writer error during CompressWithDict", func(t *testing.T) {
		t.Parallel()
		ew := &errWriter{}
		err := zCodec.CompressWithDict(ew, testData, "fastest", dict)
		if err == nil {
			t.Fatalf("expected error writing with errWriter")
		}
	})
}

func TestTrainDictionaryValidation(t *testing.T) {
	t.Parallel()

	t.Run("Empty samples list", func(t *testing.T) {
		t.Parallel()
		_, err := codec.TrainDictionary(nil, 0, "")
		if err == nil {
			t.Fatalf("expected error on nil samples")
		}
	})

	t.Run("Zero total sample bytes", func(t *testing.T) {
		t.Parallel()
		_, err := codec.TrainDictionary([][]byte{{}, {}}, 0, "")
		if err == nil {
			t.Fatalf("expected error on empty slices")
		}
	})

	t.Run("Target dict size clamping", func(t *testing.T) {
		t.Parallel()
		samples := generateDictionarySamples()
		// Negative size -> default
		dict1, err := codec.TrainDictionary(samples, -100, "")
		if err != nil || len(dict1) == 0 {
			t.Fatalf("unexpected error: %v", err)
		}

		// Extremely large size -> capped at MaxDictSize
		dict2, err := codec.TrainDictionary(samples, 50*1024*1024, "best")
		if err != nil || len(dict2) == 0 {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestDecompressionBombRejection(t *testing.T) {
	t.Parallel()

	const (
		bombPayloadSize   = 2 * 1024 * 1024 // 2 MB expanded payload
		declaredLimitSize = 512             // 512 bytes declared limit
	)

	// Create a highly repetitive payload that compresses to very few bytes
	largeData := bytes.Repeat([]byte("MICROFAT_HIGH_COMPRESSION_RATIO_REPETITIVE_PATTERN_0123456789\n"), bombPayloadSize/64)

	t.Run("Zstd decompression bomb rejected early", func(t *testing.T) {
		t.Parallel()
		zCodec, err := codec.Get(codec.AlgorithmZstd)
		if err != nil {
			t.Fatalf("Get zstd: %v", err)
		}

		var compressed bytes.Buffer
		if err := zCodec.Compress(&compressed, largeData, "best"); err != nil {
			t.Fatalf("Compress failed: %v", err)
		}

		var dest bytes.Buffer
		err = zCodec.Decompress(&dest, bytes.NewReader(compressed.Bytes()), declaredLimitSize)
		if err == nil {
			t.Fatal("expected error decompressing payload exceeding declared limit, got nil")
		}
		if !errors.Is(err, codec.ErrSizeMismatch) {
			t.Fatalf("expected ErrSizeMismatch, got %v", err)
		}
		if int64(dest.Len()) > declaredLimitSize {
			t.Fatalf("decompressed buffer exceeded limit: got %d bytes, limit was %d", dest.Len(), declaredLimitSize)
		}
	})

	t.Run("LZ4 decompression bomb rejected early", func(t *testing.T) {
		t.Parallel()
		lzCodec, err := codec.Get(codec.AlgorithmLZ4)
		if err != nil {
			t.Fatalf("Get lz4: %v", err)
		}

		var compressed bytes.Buffer
		if err := lzCodec.Compress(&compressed, largeData, "best"); err != nil {
			t.Fatalf("Compress failed: %v", err)
		}

		var dest bytes.Buffer
		err = lzCodec.Decompress(&dest, bytes.NewReader(compressed.Bytes()), declaredLimitSize)
		if err == nil {
			t.Fatal("expected error decompressing payload exceeding declared limit, got nil")
		}
		if !errors.Is(err, codec.ErrSizeMismatch) {
			t.Fatalf("expected ErrSizeMismatch, got %v", err)
		}
		if int64(dest.Len()) > declaredLimitSize {
			t.Fatalf("decompressed buffer exceeded limit: got %d bytes, limit was %d", dest.Len(), declaredLimitSize)
		}
	})

	t.Run("None decompression bomb rejected early", func(t *testing.T) {
		t.Parallel()
		nCodec, err := codec.Get(codec.AlgorithmNone)
		if err != nil {
			t.Fatalf("Get none: %v", err)
		}

		var dest bytes.Buffer
		err = nCodec.Decompress(&dest, bytes.NewReader(largeData), declaredLimitSize)
		if err == nil {
			t.Fatal("expected error decompressing payload exceeding declared limit, got nil")
		}
		if !errors.Is(err, codec.ErrSizeMismatch) {
			t.Fatalf("expected ErrSizeMismatch, got %v", err)
		}
		if int64(dest.Len()) > declaredLimitSize {
			t.Fatalf("decompressed buffer exceeded limit: got %d bytes, limit was %d", dest.Len(), declaredLimitSize)
		}
	})

	t.Run("Zstd dictionary decompression bomb rejected early", func(t *testing.T) {
		t.Parallel()
		samples := generateDictionarySamples()
		dict, err := codec.TrainDictionary(samples, 32*1024, "default")
		if err != nil {
			t.Fatalf("TrainDictionary failed: %v", err)
		}

		zCodecRaw, err := codec.Get(codec.AlgorithmZstd)
		if err != nil {
			t.Fatalf("Get zstd: %v", err)
		}
		zCodec, ok := zCodecRaw.(codec.DictCodec)
		if !ok {
			t.Fatalf("expected DictCodec")
		}

		var compressed bytes.Buffer
		if err := zCodec.CompressWithDict(&compressed, largeData, "fastest", dict); err != nil {
			t.Fatalf("CompressWithDict failed: %v", err)
		}

		var dest bytes.Buffer
		err = zCodec.DecompressWithDict(&dest, bytes.NewReader(compressed.Bytes()), declaredLimitSize, dict)
		if err == nil {
			t.Fatal("expected error decompressing payload with dict exceeding declared limit, got nil")
		}
		if !errors.Is(err, codec.ErrSizeMismatch) {
			t.Fatalf("expected ErrSizeMismatch, got %v", err)
		}
		if int64(dest.Len()) > declaredLimitSize {
			t.Fatalf("decompressed buffer exceeded limit: got %d bytes, limit was %d", dest.Len(), declaredLimitSize)
		}
	})
}


func BenchmarkCodecs(b *testing.B) {
	data := generateTestData(1024 * 1024) // 1 MB payload

	for _, name := range []string{codec.AlgorithmNone, codec.AlgorithmLZ4, codec.AlgorithmZstd} {
		name := name
		c, _ := codec.Get(name)

		var compressed bytes.Buffer
		_ = c.Compress(&compressed, data, "")
		compBytes := compressed.Bytes()

		b.Run("Decompress/"+name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				r := bytes.NewReader(compBytes)
				err := c.Decompress(io.Discard, r, int64(len(data)))
				if err != nil {
					b.Fatalf("decompress: %v", err)
				}
			}
		})
	}
}
