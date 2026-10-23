package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"slices"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureSize = 128 * 1024

func fixtureELF() []byte {
	const (
		sectionTable = 64
		sectionSize  = 64
		sectionCount = 2
		bodyOffset   = sectionTable + sectionSize*sectionCount
	)
	data := make([]byte, bodyOffset+fixtureSize)
	copy(data, "\x7fELF")
	data[elf.EI_CLASS], data[elf.EI_DATA], data[elf.EI_VERSION] = byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), 1
	binary.LittleEndian.PutUint16(data[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(data[18:], uint16(elf.EM_X86_64))
	binary.LittleEndian.PutUint32(data[20:], 1)
	binary.LittleEndian.PutUint64(data[40:], sectionTable)
	binary.LittleEndian.PutUint16(data[52:], sectionTable)
	binary.LittleEndian.PutUint16(data[58:], sectionSize)
	binary.LittleEndian.PutUint16(data[60:], sectionCount)
	section := data[sectionTable+sectionSize:]
	binary.LittleEndian.PutUint32(section[4:], uint32(elf.SHT_PROGBITS))
	binary.LittleEndian.PutUint64(section[24:], bodyOffset)
	binary.LittleEndian.PutUint64(section[32:], fixtureSize)
	for i := bodyOffset; i < len(data); i++ {
		data[i] = byte((i % 251) ^ (i / 1024))
	}
	return data
}

func fixtureInputs() []input {
	first := fixtureELF()
	second := slices.Clone(first)
	for i := 1024; i < len(second); i += sampleBytes {
		second[i] ^= 1
	}
	return []input{
		{Family: "test", Level: "v1", data: first, Bytes: len(first), SHA256: digest(first)},
		{Family: "test", Level: "v2", data: second, Bytes: len(second), SHA256: digest(second)},
	}
}

func TestELFAndSampling(t *testing.T) {
	t.Parallel()
	inputs := fixtureInputs()
	require.NoError(t, validateELF(inputs[0].data, "amd64"))
	require.Error(t, validateELF(inputs[0].data, "arm64"))
	require.Error(t, validateELF(inputs[0].data, "other"))
	require.Error(t, validateELF(nil, "amd64"))
	arm := slices.Clone(inputs[0].data)
	binary.LittleEndian.PutUint16(arm[18:], uint16(elf.EM_AARCH64))
	require.NoError(t, validateELF(arm, "arm64"))
	uniform := uniformSamples(inputs)
	require.Len(t, uniform, samplesPerFile*len(inputs))
	assert.Equal(t, inputs[0].data[:sampleBytes], uniform[0].data)
	step := len(inputs[0].data) / samplesPerFile
	assert.Equal(t, inputs[0].data[step:step+sampleBytes], uniform[1].data)
	section := sectionSamples(inputs)
	require.Len(t, section, samplesPerFile*len(inputs))
	for _, item := range section {
		assert.Len(t, item.data, sampleBytes)
	}
	assert.Empty(t, sectionSamples([]input{{data: nil}}))
	assert.Len(t, uniformSamples([]input{{data: []byte("tiny")}}), 1)
	all := candidates(inputs, []int{sampleBytes})
	require.Len(t, all, 7)
	assert.Equal(t, "none", candidateID(all[0]))
	assert.Equal(t, "current-4096", candidateID(all[1]))
}

func TestHistoryAblations(t *testing.T) {
	t.Parallel()
	block := func(value byte) []byte { return bytes.Repeat([]byte{value}, sampleBytes) }
	core, accessory, rare := block('c'), block('a'), block('r')
	samples := []sample{
		{data: core, variant: 0}, {data: accessory, variant: 0}, {data: rare, variant: 0},
		{data: core, variant: 1}, {data: accessory, variant: 1}, {data: core, variant: 1},
	}
	ordered := balanced(samples)
	require.Equal(t, []int{0, 1, 0, 1, 0, 1}, []int{
		ordered[0].variant, ordered[1].variant, ordered[2].variant,
		ordered[3].variant, ordered[4].variant, ordered[5].variant,
	})
	unique := motifs(samples)
	require.Len(t, unique, 3)
	assert.Len(t, unique[0].variants, 2)
	assert.Equal(t, 3, unique[0].count)
	assert.Equal(t, []byte{'c', 'r', 'a'}, func() []byte {
		var out []byte
		for _, item := range interleaveCoreAccessory(unique, samples) {
			out = append(out, item.data[0])
		}
		return out
	}())
	for _, policy := range []string{"current", "balanced", "deduplicated", "frequency", "core-accessory", "section"} {
		t.Run(policy, func(t *testing.T) {
			t.Parallel()
			history, total := selectedHistory(candidate{policy: policy, size: sampleBytes, samples: samples})
			assert.Equal(t, len(samples)*sampleBytes, total)
			assert.Len(t, history, sampleBytes)
			assert.Equal(t, core, history)
		})
	}
	// Repeated windows must not displace a later unique motif in deduplicated history.
	small := []sample{{data: []byte("aaaa"), variant: 0}, {data: []byte("aaaa"), variant: 1},
		{data: []byte("bbbb"), variant: 1}, {data: []byte("cccc"), variant: 1}}
	history, _ := selectedHistory(candidate{policy: "deduplicated", size: sampleBytes, samples: small})
	assert.Equal(t, "aaaabbbb", string(history))
}

func TestCompleteArtifactCandidates(t *testing.T) {
	// Serial because the allocation/timing diagnostics intentionally observe the process.
	inputs := fixtureInputs()
	stub := fixtureELF()
	opts := options{arch: "amd64", level: "fastest"}
	for _, candidate := range candidates(inputs, []int{sampleBytes}) {
		t.Run(candidateID(candidate), func(t *testing.T) {
			res, artifact := evaluate(candidate, inputs, stub, opts)
			require.Empty(t, res.Error)
			require.True(t, res.ArtifactVerified)
			require.Len(t, artifact, res.ArtifactBytes)
			var payloadBytes int
			for _, item := range res.Payloads {
				assert.True(t, item.Verified)
				payloadBytes += item.Bytes
			}
			assert.Equal(t, len(stub)+res.DictionaryBytes+payloadBytes+res.MetadataBytes, res.ArtifactBytes)
			assert.Equal(t, digest(artifact), res.ArtifactSHA256)
			idx, checks, err := pack.VerifyBinary(bytes.NewReader(artifact), int64(len(artifact)))
			require.NoError(t, err)
			require.Len(t, checks, len(inputs))
			assert.Equal(t, int64(0), idx.CreatedUnix)
			assert.Equal(t, format.FormatVersion2, idx.Version)
			_, again := evaluate(candidate, inputs, stub, opts)
			assert.Equal(t, artifact, again, "same settings produce deterministic complete artifacts")
			if idx.DictionarySize > 0 {
				artifact[idx.DictionaryOffset] ^= 1
				_, _, err = pack.VerifyBinary(bytes.NewReader(artifact), int64(len(artifact)))
				require.ErrorIs(t, err, format.ErrDictionaryCorrupted)
			} else {
				artifact[idx.Variants[0].Offset] ^= 1
				_, checks, err = pack.VerifyBinary(bytes.NewReader(artifact), int64(len(artifact)))
				require.NoError(t, err)
				assert.False(t, checks[0].Valid)
			}
		})
	}
	_, _, err := train(candidate{policy: "current"}, "better")
	require.Error(t, err)
	_, _, err = train(candidate{policy: "balanced", samples: uniformSamples(inputs), size: sampleBytes}, "invalid")
	require.Error(t, err)
	bad := inputs[0]
	bad.SHA256 = "wrong"
	_, _, err = compressAndVerify(codec.NewZstdCodec(), bad, nil, "fastest")
	require.ErrorContains(t, err, "exact round trip")
	_, _, err = compressAndVerify(codec.NewZstdCodec(), inputs[0], nil, "invalid")
	require.Error(t, err)
	res, _ := evaluate(candidate{policy: "current"}, inputs, stub, opts)
	assert.NotEmpty(t, res.Error)
	res, _ = evaluate(candidate{policy: "none"}, inputs, stub, options{arch: "amd64", level: "invalid"})
	assert.NotEmpty(t, res.Error)
	assert.Empty(t, smallest([]result{{ID: "failed", Error: "error"}}))
	assert.Equal(t, "small", smallest([]result{{ID: "large", ArtifactBytes: 10}, {ID: "small", ArtifactBytes: 1}}))
}
