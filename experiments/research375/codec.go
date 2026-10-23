package main

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/klauspost/compress/zstd"
)

type payload struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
	Data   []byte `json:"-"`
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304,G703 -- explicit trusted corpus or private workspace path.
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, limit+1))
	closeErr := f.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("input %q exceeds %d byte limit", path, limit)
	}
	return data, nil
}

func loadPayload(path string) (payload, error) {
	data, err := readBounded(path, maxInputBytes)
	if err != nil {
		return payload{}, err
	}
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return payload{}, err
	}
	machine := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[runtime.GOARCH]
	if machine == elf.EM_NONE || f.Machine != machine || runtime.GOOS != "linux" {
		return payload{}, errors.New("requires native linux amd64/arm64 ELF")
	}
	if _, err := buildinfo.Read(bytes.NewReader(data)); err != nil {
		return payload{}, fmt.Errorf("require real Go ELF: %w", err)
	}
	return payload{Path: path, SHA256: digest(data), Bytes: len(data), Data: data}, nil
}

func encode(data, dict []byte, level string, threads int) ([]byte, error) {
	l, err := codec.NewZstdCodec().MapLevel(level)
	if err != nil {
		return nil, err
	}
	opts := []zstd.EOption{zstd.WithEncoderLevel(l)}
	if threads != -1 {
		opts = append(opts, zstd.WithEncoderConcurrency(threads))
	}
	if len(dict) > 0 {
		opts = append(opts, zstd.WithEncoderDict(dict))
	}
	var b bytes.Buffer
	w, err := zstd.NewWriter(&b, opts...)
	if err != nil {
		return nil, err
	}
	_, writeErr := w.Write(data)
	if err := errors.Join(writeErr, w.Close()); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("output exceeds declared byte bound")
	}
	return b.Buffer.Write(p)
}

func decode(data, dict []byte, expectedBytes int, expectedHash string, threads int) ([]byte, int64, error) {
	start := time.Now()
	opts := []zstd.DOption{
		zstd.WithDecoderMaxMemory(codec.DefaultDecoderMaxMemory),
		zstd.WithDecoderMaxWindow(codec.DefaultDecoderMaxWindow),
	}
	if threads != -1 {
		opts = append(opts, zstd.WithDecoderConcurrency(threads))
	}
	if len(dict) > 0 {
		opts = append(opts, zstd.WithDecoderDicts(dict))
	}
	r, err := zstd.NewReader(bytes.NewReader(data), opts...)
	if err != nil {
		return nil, time.Since(start).Nanoseconds(), err
	}
	defer r.Close()
	b := boundedBuffer{limit: expectedBytes}
	_, err = io.Copy(&b, r)
	if err == nil && (b.Len() != expectedBytes || digest(b.Bytes()) != expectedHash) {
		err = errors.New("decoded size or SHA256 mismatch")
	}
	return b.Bytes(), time.Since(start).Nanoseconds(), err
}
