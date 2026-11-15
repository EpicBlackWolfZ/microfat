package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
)

const (
	containerMagic = "MF47POC1"
	maxHeader      = 64 * kib
	maxPath        = 16 * mib
	maxInput       = 96 * mib
	maxGroup       = maxVariants * maxInput
	maxContainer   = 4 * maxGroup
	framePrefix    = len(containerMagic) + 8
	pathResidual   = byte(0)
	pathShared     = byte(1)
)

type span struct {
	shared bool
	offset int
	size   int
}

type target struct {
	level    string
	original []byte
	residual []byte
	path     []span
}

type model struct {
	method  string
	base    string
	pool    []byte
	targets []target
}

func appendSpan(path []span, next span) []span {
	if len(path) > 0 {
		last := &path[len(path)-1]
		if last.shared == next.shared && last.offset+last.size == next.offset {
			last.size += next.size
			return path
		}
	}
	return append(path, next)
}

func sharingModel(inv inventory, levels []string, inputs [][]byte, method string) model {
	m := model{method: method}
	for _, record := range inv.records {
		if bitsSet(record.prevalence) > 1 {
			record.poolOffset = len(m.pool)
			m.pool = append(m.pool, record.data...)
		}
	}
	for i, records := range inv.paths {
		t := target{level: levels[i], original: inputs[i]}
		for _, record := range records {
			s := span{shared: record.poolOffset >= 0, offset: record.poolOffset, size: len(record.data)}
			if !s.shared {
				s.offset = len(t.residual)
				t.residual = append(t.residual, record.data...)
			}
			t.path = appendSpan(t.path, s)
		}
		m.targets = append(m.targets, t)
	}
	return m
}

func baseModel(inputs [][]byte, levels []string, base int) model {
	m := model{method: methodBase, base: levels[base], pool: inputs[base]}
	buckets := make(map[[sha256.Size]byte][]extent)
	for _, chunk := range contentChunks(m.pool) {
		digest := sha256.Sum256(m.pool[chunk.start:chunk.end])
		buckets[digest] = append(buckets[digest], chunk)
	}
	for i, input := range inputs {
		t := target{level: levels[i], original: input}
		if i == base {
			t.path = []span{{shared: true, size: len(input)}}
		} else {
			for _, chunk := range contentChunks(input) {
				data := input[chunk.start:chunk.end]
				s := span{offset: len(t.residual), size: len(data)}
				for _, candidate := range buckets[sha256.Sum256(data)] {
					if bytes.Equal(data, m.pool[candidate.start:candidate.end]) {
						s.shared, s.offset = true, candidate.start
						break
					}
				}
				if !s.shared {
					t.residual = append(t.residual, data...)
				}
				t.path = appendSpan(t.path, s)
			}
		}
		m.targets = append(m.targets, t)
	}
	return m
}

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func encodePath(path []span) []byte {
	var result []byte
	for _, s := range path {
		kind := pathResidual
		if s.shared {
			kind = pathShared
		}
		result = append(result, kind)
		result = binary.AppendUvarint(result, uint64(s.offset)) // #nosec G115 -- internal bounded extents
		result = binary.AppendUvarint(result, uint64(s.size))   // #nosec G115 -- internal bounded extents
	}
	return result
}

type frame struct {
	RawBytes int    `json:"raw_bytes"`
	Bytes    int    `json:"bytes"`
	SHA256   string `json:"sha256"`
}

type targetHeader struct {
	Level          string `json:"level"`
	Bytes          int    `json:"bytes"`
	SHA256         string `json:"sha256"`
	Residual       frame  `json:"residual"`
	Path           frame  `json:"path"`
	PathCompressed bool   `json:"path_compressed"`
}

type wireHeader struct {
	Schema  int            `json:"schema"`
	Method  string         `json:"method"`
	Base    string         `json:"base,omitempty"`
	Pool    frame          `json:"pool"`
	Targets []targetHeader `json:"targets"`
}

func compressed(data []byte, level string) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var out bytes.Buffer
	if err := codec.NewZstdCodec().Compress(&out, data, level); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func encodeModel(m model, level string) ([]byte, wireHeader, error) {
	pool, err := compressed(m.pool, level)
	if err != nil {
		return nil, wireHeader{}, err
	}
	h := wireHeader{Schema: 1, Method: m.method, Base: m.base, Pool: frame{len(m.pool), len(pool), digest(m.pool)}}
	payload := append([]byte(nil), pool...)
	for _, t := range m.targets {
		residual, compressErr := compressed(t.residual, level)
		if compressErr != nil {
			return nil, wireHeader{}, compressErr
		}
		path := encodePath(t.path)
		packedPath, compressErr := compressed(path, level)
		if compressErr != nil {
			return nil, wireHeader{}, compressErr
		}
		useCompressed := len(packedPath) < len(path)
		if !useCompressed {
			packedPath = path
		}
		h.Targets = append(h.Targets, targetHeader{Level: t.level, Bytes: len(t.original), SHA256: digest(t.original),
			Residual: frame{len(t.residual), len(residual), digest(t.residual)},
			Path:     frame{len(path), len(packedPath), digest(path)}, PathCompressed: useCompressed})
		payload = append(payload, residual...)
		payload = append(payload, packedPath...)
	}
	header, err := json.Marshal(h)
	if err != nil {
		return nil, wireHeader{}, err
	}
	result := append([]byte(containerMagic), binary.LittleEndian.AppendUint64(nil, uint64(len(header)))...)
	result = append(result, header...)
	result = append(result, payload...)
	checksum := sha256.Sum256(header)
	result = append(result, checksum[:]...)
	return result, h, nil
}

func parseContainer(data []byte) (wireHeader, []byte, error) {
	if len(data) < framePrefix+sha256.Size || len(data) > maxContainer || string(data[:len(containerMagic)]) != containerMagic {
		return wireHeader{}, nil, errors.New("invalid container bounds or magic")
	}
	n := binary.LittleEndian.Uint64(data[len(containerMagic):framePrefix])
	available := uint64(len(data) - framePrefix - sha256.Size) // #nosec G115 -- minimum container length checked above
	if n == 0 || n > maxHeader || n > available {
		return wireHeader{}, nil, errors.New("invalid header size")
	}
	header := data[framePrefix : framePrefix+int(n)] // #nosec G115 -- bounded header size
	expected := sha256.Sum256(header)
	if !bytes.Equal(expected[:], data[len(data)-sha256.Size:]) {
		return wireHeader{}, nil, errors.New("header integrity failure")
	}
	var h wireHeader
	if err := json.Unmarshal(header, &h, json.RejectUnknownMembers(true)); err != nil {
		return wireHeader{}, nil, err
	}
	payload := data[framePrefix+int(n) : len(data)-sha256.Size] // #nosec G115 -- bounded header size
	if err := validateHeader(h, len(payload)); err != nil {
		return wireHeader{}, nil, err
	}
	return h, payload, nil
}

func validFrame(f frame, limit int) bool {
	decoded, err := hex.DecodeString(f.SHA256)
	return err == nil && len(decoded) == sha256.Size && f.RawBytes >= 0 && f.RawBytes <= limit &&
		f.Bytes >= 0 && f.Bytes <= maxContainer && (f.RawBytes == 0) == (f.Bytes == 0)
}

func validateHeader(h wireHeader, size int) error {
	if h.Schema != 1 || len(h.Targets) == 0 || len(h.Targets) > maxVariants || !validFrame(h.Pool, maxGroup) || h.Pool.Bytes > size {
		return errors.New("invalid pool/header")
	}
	switch h.Method {
	case methodSections, methodSmall, methodLarge, methodCDC:
		if h.Base != "" {
			return errors.New("unexpected base for range sharing")
		}
	case methodBase:
		if h.Pool.RawBytes == 0 {
			return errors.New("empty base")
		}
	default:
		return errors.New("unknown representation method")
	}
	used := h.Pool.Bytes
	seen := make(map[string]bool)
	for _, t := range h.Targets {
		if !validTargetHeader(t) || seen[t.Level] {
			return errors.New("invalid target header")
		}
		seen[t.Level] = true
		if t.Residual.Bytes > size-used || t.Path.Bytes > size-used-t.Residual.Bytes {
			return errors.New("frame exceeds remaining payload")
		}
		used += t.Residual.Bytes + t.Path.Bytes
	}
	if h.Method == methodBase && !seen[h.Base] {
		return errors.New("base target not present")
	}
	if used != size {
		return errors.New("payload length mismatch")
	}
	return nil
}

func validTargetHeader(t targetHeader) bool {
	checksum, err := hex.DecodeString(t.SHA256)
	return err == nil && len(checksum) == sha256.Size && t.Bytes > 0 && t.Bytes <= maxInput && t.Level != "" &&
		validFrame(t.Residual, maxInput) && validFrame(t.Path, maxPath) && t.Path.RawBytes > 0 &&
		(t.PathCompressed || t.Path.Bytes == t.Path.RawBytes)
}

func decodeFrame(encoded []byte, f frame, compressedFrame bool) ([]byte, error) {
	var out bytes.Buffer
	if compressedFrame && len(encoded) > 0 {
		if err := codec.NewZstdCodec().Decompress(&out, bytes.NewReader(encoded), int64(f.RawBytes)); err != nil {
			return nil, err
		}
	} else {
		out.Write(encoded)
	}
	if out.Len() != f.RawBytes || digest(out.Bytes()) != f.SHA256 {
		return nil, errors.New("frame integrity failure")
	}
	return out.Bytes(), nil
}

func reconstruct(path, pool, residual []byte, size int) ([]byte, error) {
	if size <= 0 || size > maxInput || len(path) > maxPath {
		return nil, errors.New("invalid reconstruction bounds")
	}
	reader := bytes.NewReader(path)
	out := make([]byte, 0, size)
	for count := 0; reader.Len() > 0; count++ {
		if count >= maxChunks {
			return nil, errors.New("path traversal count exceeds bound")
		}
		kind, _ := reader.ReadByte()
		offset, errOffset := binary.ReadUvarint(reader)
		length, errLength := binary.ReadUvarint(reader)
		source := residual
		if kind == pathShared {
			source = pool
		}
		remaining := uint64(size - len(out)) // #nosec G115 -- every preceding append is bounded by the remaining output
		if errOffset != nil || errLength != nil || kind > pathShared || length == 0 ||
			offset > uint64(len(source)) || length > uint64(len(source))-offset || length > remaining {
			return nil, errors.New("invalid or expanding path range")
		}
		out = append(out, source[int(offset):int(offset+length)]...) // #nosec G115 -- bounded above
	}
	if len(out) != size {
		return nil, io.ErrUnexpectedEOF
	}
	return out, nil
}

func decodeTarget(data []byte, level string) ([]byte, error) {
	h, payload, err := parseContainer(data)
	if err != nil {
		return nil, err
	}
	pool, err := decodeFrame(payload[:h.Pool.Bytes], h.Pool, true)
	if err != nil {
		return nil, fmt.Errorf("pool: %w", err)
	}
	position := h.Pool.Bytes
	for _, t := range h.Targets {
		end := position + t.Residual.Bytes + t.Path.Bytes
		if t.Level != level {
			position = end
			continue
		}
		residual, decodeErr := decodeFrame(payload[position:position+t.Residual.Bytes], t.Residual, true)
		if decodeErr != nil {
			return nil, decodeErr
		}
		path, decodeErr := decodeFrame(payload[position+t.Residual.Bytes:end], t.Path, t.PathCompressed)
		if decodeErr != nil {
			return nil, decodeErr
		}
		out, decodeErr := reconstruct(path, pool, residual, t.Bytes)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if digest(out) != t.SHA256 {
			return nil, errors.New("target integrity failure")
		}
		return out, nil
	}
	return nil, errors.New("target not present")
}
