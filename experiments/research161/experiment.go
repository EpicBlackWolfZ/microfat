package main

import (
	"bytes"
	"fmt"
	"runtime"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/klauspost/compress/zstd"
)

type payloadResult struct {
	Level      string `json:"level"`
	Bytes      int    `json:"compressed_bytes"`
	CompressNS int64  `json:"compress_wall_ns_diagnostic"`
	DecodeNS   int64  `json:"decode_wall_ns_diagnostic"`
	Verified   bool   `json:"byte_exact_and_sha256_verified"`
}

type result struct {
	ID               string          `json:"id"`
	HistoryBudget    int             `json:"history_budget_bytes"`
	SampleBytes      int             `json:"sample_bytes"`
	SampleCount      int             `json:"sample_count"`
	UniqueSamples    int             `json:"unique_sample_count"`
	CoreSamples      int             `json:"core_sample_count"`
	HistoryBytes     int             `json:"history_bytes"`
	HistorySHA256    string          `json:"history_sha256"`
	DictionaryBytes  int             `json:"dictionary_bytes"`
	DictionarySHA256 string          `json:"dictionary_sha256"`
	TrainNS          int64           `json:"train_wall_ns_diagnostic"`
	EncodeNS         int64           `json:"train_and_assemble_wall_ns_diagnostic"`
	AllocatedBytes   uint64          `json:"train_and_assemble_allocated_bytes_diagnostic"`
	Payloads         []payloadResult `json:"payloads"`
	MetadataBytes    int             `json:"index_and_trailer_bytes"`
	ArtifactBytes    int             `json:"artifact_bytes"`
	ArtifactSHA256   string          `json:"artifact_sha256"`
	ArtifactVerified bool            `json:"artifact_verified"`
	Error            string          `json:"error,omitempty"`
}

func train(c candidate, level string) ([]byte, result, error) {
	res := result{ID: candidateID(c), HistoryBudget: c.size, SampleCount: len(c.samples)}
	if c.policy == "none" {
		return nil, res, nil
	}
	history, total := selectedHistory(c)
	res.SampleBytes, res.HistoryBytes, res.HistorySHA256 = total, len(history), digest(history)
	unique := motifs(c.samples)
	res.UniqueSamples = len(unique)
	variants := make(map[int]bool)
	for _, item := range c.samples {
		variants[item.variant] = true
	}
	for _, item := range unique {
		if len(item.variants) == len(variants) {
			res.CoreSamples++
		}
	}
	contents := make([][]byte, 0, len(c.samples))
	for _, item := range c.samples {
		contents = append(contents, item.data)
	}
	start := time.Now()
	var dictionary []byte
	var err error
	if c.policy == "current" {
		dictionary, err = codec.TrainDictionary(contents, c.size, level)
	} else {
		encLevel, mapErr := codec.NewZstdCodec().MapLevel(level)
		if mapErr != nil {
			return nil, res, mapErr
		}
		dictionary, err = zstd.BuildDict(zstd.BuildDictOptions{
			ID: codec.DefaultDictionaryID, History: history, Contents: contents, Level: encLevel,
		})
	}
	res.TrainNS = time.Since(start).Nanoseconds()
	res.DictionaryBytes, res.DictionarySHA256 = len(dictionary), digest(dictionary)
	return dictionary, res, err
}

func evaluate(c candidate, inputs []input, stub []byte, opts options) (result, []byte) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	dictionary, res, err := train(c, opts.level)
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	artifact, err := assemble(inputs, stub, dictionary, opts, &res)
	res.EncodeNS = time.Since(start).Nanoseconds()
	runtime.ReadMemStats(&after)
	res.AllocatedBytes = after.TotalAlloc - before.TotalAlloc
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	_, checks, err := pack.VerifyBinary(bytes.NewReader(artifact), int64(len(artifact)))
	if err != nil {
		res.Error = fmt.Sprintf("complete artifact verification: %v", err)
		return res, nil
	}
	for _, check := range checks {
		if !check.Valid {
			res.Error = fmt.Sprintf("complete artifact variant %s: %s", check.Level, check.ErrorString)
			return res, nil
		}
	}
	res.ArtifactBytes, res.ArtifactSHA256, res.ArtifactVerified = len(artifact), digest(artifact), true
	return res, artifact
}

func assemble(inputs []input, stub, dictionary []byte, opts options, res *result) ([]byte, error) {
	var artifact bytes.Buffer
	artifact.Write(stub)
	idx := &format.Index{
		Version: format.FormatVersion2, AppName: "research161", TargetOS: "linux", TargetArch: opts.arch,
	}
	if len(dictionary) > 0 {
		idx.DictionaryOffset = int64(artifact.Len())
		idx.DictionarySize = int64(len(dictionary))
		idx.DictionarySHA256 = digest(dictionary)
		idx.DictionaryID = codec.DefaultDictionaryID
		artifact.Write(dictionary)
	}
	zcodec := codec.NewZstdCodec()
	for _, item := range inputs {
		payload, diagnostics, err := compressAndVerify(zcodec, item, dictionary, opts.level)
		res.Payloads = append(res.Payloads, diagnostics)
		if err != nil {
			return nil, err
		}
		idx.Variants = append(idx.Variants, format.VariantEntry{
			Level: item.Level, Offset: int64(artifact.Len()), CompressedSize: int64(len(payload)),
			UncompressedSize: int64(len(item.data)), SHA256: item.SHA256, Compression: codec.AlgorithmZstd,
		})
		artifact.Write(payload)
	}
	metadataBytes, err := format.WriteIndexAndTrailer(&artifact, idx, int64(artifact.Len()))
	if err != nil {
		return nil, err
	}
	res.MetadataBytes = int(metadataBytes)
	return artifact.Bytes(), nil
}

func compressAndVerify(zcodec *codec.ZstdCodec, item input, dictionary []byte, level string) ([]byte, payloadResult, error) {
	res := payloadResult{Level: item.Level}
	var compressed bytes.Buffer
	start := time.Now()
	if err := zcodec.CompressWithDict(&compressed, item.data, level, dictionary); err != nil {
		return nil, res, err
	}
	res.CompressNS = time.Since(start).Nanoseconds()
	res.Bytes = compressed.Len()
	var decoded bytes.Buffer
	start = time.Now()
	if err := zcodec.DecompressWithDict(&decoded, bytes.NewReader(compressed.Bytes()), int64(len(item.data)), dictionary); err != nil {
		return nil, res, err
	}
	res.DecodeNS = time.Since(start).Nanoseconds()
	if !bytes.Equal(item.data, decoded.Bytes()) || digest(decoded.Bytes()) != item.SHA256 {
		return nil, res, fmt.Errorf("variant %s failed exact round trip", item.Level)
	}
	res.Verified = true
	return compressed.Bytes(), res, nil
}
