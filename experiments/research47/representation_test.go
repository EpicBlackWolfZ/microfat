package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixtureModel(t *testing.T) model {
	t.Helper()
	inputs, levels := fixtureInputs()
	inv, err := buildInventory(inputs, methodSmall)
	require.NoError(t, err)
	return sharingModel(inv, levels, inputs, methodSmall)
}

func replaceHeader(t *testing.T, data []byte, edit func(*wireHeader)) []byte {
	t.Helper()
	h, payload, err := parseContainer(data)
	require.NoError(t, err)
	edit(&h)
	header, err := json.Marshal(h)
	require.NoError(t, err)
	return wrapHeader(header, payload)
}

func wrapHeader(header, payload []byte) []byte {
	data := append([]byte(containerMagic), binary.LittleEndian.AppendUint64(nil, uint64(len(header)))...)
	data = append(data, header...)
	data = append(data, payload...)
	sum := sha256.Sum256(header)
	return append(data, sum[:]...)
}

func TestRepresentationsRoundtripExactly(t *testing.T) {
	t.Parallel()
	inputs, levels := fixtureInputs()
	for _, method := range []string{methodSections, methodSmall, methodLarge, methodCDC} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			inv, err := buildInventory(inputs, method)
			require.NoError(t, err)
			m := sharingModel(inv, levels, inputs, method)
			data, _, err := encodeModel(m, defaultLevel)
			require.NoError(t, err)
			require.NoError(t, roundtripModel(data, m))
			again, _, err := encodeModel(m, defaultLevel)
			require.NoError(t, err)
			require.Equal(t, data, again)
		})
	}
	for base := range inputs {
		m := baseModel(inputs, levels, base)
		data, _, err := encodeModel(m, defaultLevel)
		require.NoError(t, err)
		require.NoError(t, roundtripModel(data, m))
	}
	// A no-sharing model must retain every byte in target residuals.
	unique := [][]byte{bytes.Repeat([]byte{1}, fixedSmall), bytes.Repeat([]byte{2}, fixedSmall)}
	inv, err := buildInventory(unique, methodSmall)
	require.NoError(t, err)
	m := sharingModel(inv, levels[:2], unique, methodSmall)
	require.Empty(t, m.pool)
	data, _, err := encodeModel(m, defaultLevel)
	require.NoError(t, err)
	require.NoError(t, roundtripModel(data, m))
}

func TestContainerRejectsInvalidFramingAndMetadata(t *testing.T) {
	t.Parallel()
	data, _, err := encodeModel(fixtureModel(t), defaultLevel)
	require.NoError(t, err)
	for _, raw := range [][]byte{nil, []byte("bad"), bytes.Repeat([]byte{0}, framePrefix+sha256.Size)} {
		_, _, err = parseContainer(raw)
		require.Error(t, err)
	}
	for _, size := range []uint64{0, maxHeader + 1, uint64(len(data))} {
		raw := bytes.Clone(data)
		binary.LittleEndian.PutUint64(raw[len(containerMagic):], size)
		_, _, err = parseContainer(raw)
		require.Error(t, err)
	}
	raw := bytes.Clone(data)
	raw[len(raw)-1] ^= 1
	_, _, err = parseContainer(raw)
	require.Error(t, err)
	_, _, err = parseContainer(wrapHeader([]byte(`{"schema":1,"schema":1}`), nil))
	require.Error(t, err)
	_, _, err = parseContainer(wrapHeader([]byte(`{"extra":1}`), nil))
	require.Error(t, err)
	_, _, err = parseContainer(wrapHeader([]byte(`{`), nil))
	require.Error(t, err)
	for _, tc := range []struct {
		name string
		edit func(*wireHeader)
	}{
		{"schema", func(h *wireHeader) { h.Schema++ }},
		{"method", func(h *wireHeader) { h.Method = "unknown" }},
		{"unexpected-base", func(h *wireHeader) { h.Base = "v1" }},
		{"missing-base", func(h *wireHeader) { h.Method = methodBase; h.Base = "missing" }},
		{"empty-base", func(h *wireHeader) { h.Method = methodBase; h.Pool = frame{SHA256: digest(nil)} }},
		{"no-target", func(h *wireHeader) { h.Targets = nil }},
		{"too-many", func(h *wireHeader) { h.Targets = append(h.Targets, h.Targets[0]) }},
		{"pool-size", func(h *wireHeader) { h.Pool.RawBytes = maxGroup + 1 }},
		{"pool-hash", func(h *wireHeader) { h.Pool.SHA256 = "bad" }},
		{"target-hash", func(h *wireHeader) { h.Targets[0].SHA256 = "bad" }},
		{"target-size", func(h *wireHeader) { h.Targets[0].Bytes = maxInput + 1 }},
		{"empty-level", func(h *wireHeader) { h.Targets[0].Level = "" }},
		{"duplicate", func(h *wireHeader) { h.Targets[1].Level = h.Targets[0].Level }},
		{"path-bound", func(h *wireHeader) { h.Targets[0].Path.RawBytes = maxPath + 1 }},
		{"path-empty", func(h *wireHeader) { h.Targets[0].Path = frame{SHA256: digest(nil)} }},
		{"raw-path-length", func(h *wireHeader) { h.Targets[0].PathCompressed = false; h.Targets[0].Path.Bytes++ }},
		{"payload-length", func(h *wireHeader) { h.Targets[0].Residual.Bytes++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseContainer(replaceHeader(t, data, tc.edit))
			require.Error(t, err)
		})
	}
}

func TestDecodeRejectsCorruptionAndBadPaths(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t)
	data, h, err := encodeModel(m, defaultLevel)
	require.NoError(t, err)
	_, err = decodeTarget(data, "missing")
	require.Error(t, err)
	_, err = decodeTarget(nil, "v1")
	require.Error(t, err)
	_, payload, err := parseContainer(data)
	require.NoError(t, err)
	headerStart := len(data) - len(payload) - sha256.Size
	for _, position := range []int{0, h.Pool.Bytes, h.Pool.Bytes + h.Targets[0].Residual.Bytes} {
		raw := bytes.Clone(data)
		raw[headerStart+position] ^= 0xff
		_, err = decodeTarget(raw, "v1")
		require.Error(t, err)
	}
	raw := replaceHeader(t, data, func(h *wireHeader) { h.Targets[0].SHA256 = digest([]byte("wrong")) })
	_, err = decodeTarget(raw, "v1")
	require.ErrorContains(t, err, "target integrity")
	for _, path := range [][]byte{
		{pathShared}, {pathShared, 0xff, 0xff, 0xff}, {2, 0, 1}, {pathShared, 0, 0},
		{pathShared, 2, 1}, {pathShared, 0, 2}, {pathResidual, 0, 1}, nil,
	} {
		_, err = reconstruct(path, []byte{1}, nil, 1)
		require.Error(t, err)
	}
	_, err = reconstruct(bytes.Repeat([]byte{pathShared, 0, 1}, maxChunks+1), []byte{1}, nil, maxChunks+1)
	require.Error(t, err)
	_, err = reconstruct(nil, nil, nil, 0)
	require.Error(t, err)
	_, err = decodeFrame([]byte{1}, frame{RawBytes: 1, Bytes: 1, SHA256: digest(nil)}, false)
	require.Error(t, err)
	_, err = decodeFrame(nil, frame{RawBytes: 1, SHA256: digest(nil)}, false)
	require.Error(t, err)
	// Authenticated but semantically invalid ranges are rejected before output publication.
	badModel := m
	badModel.targets = append([]target(nil), m.targets...)
	badModel.targets[0].path = []span{{shared: true, offset: len(m.pool), size: 1}}
	bad, _, err := encodeModel(badModel, defaultLevel)
	require.NoError(t, err)
	_, err = decodeTarget(bad, "v1")
	require.Error(t, err)
	require.Error(t, roundtripModel(bad, badModel))
	changed := m
	changed.targets = append([]target(nil), m.targets...)
	changed.targets[0].original = []byte("changed")
	require.ErrorContains(t, roundtripModel(data, changed), "exact reconstruction")
}

func TestEncoderErrorsAndEmptyFrames(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t)
	_, _, err := encodeModel(m, "invalid")
	require.Error(t, err)
	_, _, err = encodeModel(model{targets: []target{{original: []byte{1}, residual: []byte{1}}}}, "invalid")
	require.Error(t, err)
	_, _, err = encodeModel(model{targets: []target{{path: []span{{size: 1}}}}}, "invalid")
	require.Error(t, err)
	_, _, err = encodeModel(model{method: string([]byte{0xff})}, defaultLevel)
	require.Error(t, err)
	result, err := compressed(nil, "invalid")
	require.NoError(t, err)
	require.Empty(t, result)
	_, err = decodeFrame(nil, frame{SHA256: digest(nil)}, true)
	require.NoError(t, err)
}
