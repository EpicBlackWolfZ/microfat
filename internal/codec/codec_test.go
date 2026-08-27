package codec_test

import (
	"bytes"
	"errors"
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
