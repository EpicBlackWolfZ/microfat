package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testBodySize = 12 * kib
	testSHSize   = 64
	testSHCount  = 3
	testTextName = 1
	testNames    = "\x00.text\x00.shstrtab\x00"
)

func fixtureELF(body []byte) []byte {
	const (
		elfHeaderSize = 64
		machineAMD64  = 62
		namesIndex    = 7
		programSize   = 56
		loadAddress   = 0x400000
		loadFlags     = 5
	)
	if len(body) < elfHeaderSize {
		body = append(bytes.Clone(body), make([]byte, elfHeaderSize-len(body))...)
	}
	sectionOffset := elfHeaderSize + len(body) + len(testNames)
	data := make([]byte, sectionOffset+testSHCount*testSHSize)
	copy(data, []byte("\x7fELF\x02\x01\x01"))
	binary.LittleEndian.PutUint16(data[16:], 2)
	binary.LittleEndian.PutUint16(data[18:], machineAMD64)
	binary.LittleEndian.PutUint32(data[20:], 1)
	binary.LittleEndian.PutUint64(data[24:], loadAddress+elfHeaderSize)
	binary.LittleEndian.PutUint64(data[32:], elfHeaderSize)
	binary.LittleEndian.PutUint64(data[40:], uint64(sectionOffset))
	binary.LittleEndian.PutUint16(data[52:], elfHeaderSize)
	binary.LittleEndian.PutUint16(data[54:], programSize)
	binary.LittleEndian.PutUint16(data[56:], 1)
	binary.LittleEndian.PutUint16(data[58:], testSHSize)
	binary.LittleEndian.PutUint16(data[60:], testSHCount)
	binary.LittleEndian.PutUint16(data[62:], 2)
	copy(data[elfHeaderSize:], body)
	program := data[elfHeaderSize:]
	binary.LittleEndian.PutUint32(program, 1)
	binary.LittleEndian.PutUint32(program[4:], loadFlags)
	binary.LittleEndian.PutUint64(program[8:], 0)
	binary.LittleEndian.PutUint64(program[16:], loadAddress)
	binary.LittleEndian.PutUint64(program[24:], loadAddress)
	binary.LittleEndian.PutUint64(program[32:], uint64(len(data)))
	binary.LittleEndian.PutUint64(program[40:], uint64(len(data)))
	binary.LittleEndian.PutUint64(program[48:], fixedLarge)
	copy(data[elfHeaderSize+len(body):], testNames)
	text := data[sectionOffset+testSHSize:]
	binary.LittleEndian.PutUint32(text, testTextName)
	binary.LittleEndian.PutUint32(text[4:], 1)
	binary.LittleEndian.PutUint64(text[24:], elfHeaderSize)
	binary.LittleEndian.PutUint64(text[32:], uint64(len(body)))
	names := data[sectionOffset+2*testSHSize:]
	binary.LittleEndian.PutUint32(names, namesIndex)
	binary.LittleEndian.PutUint32(names[4:], 3)
	binary.LittleEndian.PutUint64(names[24:], uint64(elfHeaderSize+len(body)))
	binary.LittleEndian.PutUint64(names[32:], uint64(len(testNames)))
	return data
}

func fixtureInputs() ([][]byte, []string) {
	body := make([]byte, testBodySize)
	for i := range body {
		body[i] = byte((i*31 + i/47) % 251)
	}
	var inputs [][]byte
	for i := range maxVariants {
		data := bytes.Clone(body)
		data[kib+i] ^= byte(i + 1)
		inputs = append(inputs, fixtureELF(data))
	}
	return inputs, []string{"v1", "v2", "v3"}
}

func TestChunkPartitionsCoverEveryByte(t *testing.T) {
	t.Parallel()
	inputs, _ := fixtureInputs()
	for _, method := range []string{methodSections, methodSmall, methodLarge, methodCDC} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			chunks, err := splitChunks(inputs[0], method)
			require.NoError(t, err)
			position := 0
			for _, chunk := range chunks {
				require.Equal(t, position, chunk.start)
				require.Greater(t, chunk.end, chunk.start)
				position = chunk.end
			}
			require.Equal(t, len(inputs[0]), position)
			again, err := splitChunks(inputs[0], method)
			require.NoError(t, err)
			require.Equal(t, chunks, again)
		})
	}
	_, err := splitChunks(inputs[0], "unknown")
	require.Error(t, err)
	require.Empty(t, contentChunks(nil))
	require.Empty(t, fixedChunks(0, fixedSmall))
}

func TestInventoryDistinctVariantPrevalenceAndLimits(t *testing.T) {
	t.Parallel()
	a, b, c := bytes.Repeat([]byte{'a'}, fixedSmall), bytes.Repeat([]byte{'b'}, fixedSmall), bytes.Repeat([]byte{'c'}, fixedSmall)
	inputs := [][]byte{bytes.Join([][]byte{a, a, b}, nil), bytes.Join([][]byte{a, b}, nil), bytes.Join([][]byte{a, c}, nil)}
	inv, err := buildInventory(inputs, methodSmall)
	require.NoError(t, err)
	require.Len(t, inv.records, maxVariants)
	require.EqualValues(t, 7*fixedSmall, inv.bytes)
	require.EqualValues(t, 6*fixedSmall, inv.shared)
	require.EqualValues(t, 4*fixedSmall, inv.core)
	require.EqualValues(t, maxVariants*fixedSmall, inv.unique)
	_, err = buildInventoryLimit(inputs, methodSmall, 1)
	require.Error(t, err)
	_, err = buildInventory(inputs, "bad")
	require.Error(t, err)
	_, err = buildInventory(nil, methodSmall)
	require.Error(t, err)
	_, err = buildInventory(make([][]byte, maxVariants+1), methodSmall)
	require.Error(t, err)
}

func TestSectionBoundsAndOverlaps(t *testing.T) {
	t.Parallel()
	_, err := sectionChunks([]byte("not ELF"))
	require.Error(t, err)
	data := fixtureELF(bytes.Repeat([]byte{1}, kib))
	offset := int(binary.LittleEndian.Uint64(data[40:]))
	for _, tc := range []struct {
		name string
		edit func([]byte)
		bad  bool
	}{
		{"outside", func(b []byte) { binary.LittleEndian.PutUint64(b[offset+testSHSize+32:], maxInput) }, true},
		{"overlap", func(b []byte) { binary.LittleEndian.PutUint64(b[offset+2*testSHSize+24:], 64) }, true},
		{"nobits", func(b []byte) { binary.LittleEndian.PutUint32(b[offset+testSHSize+4:], 8) }, false},
		{"gap", func(b []byte) {
			binary.LittleEndian.PutUint64(b[offset+testSHSize+24:], 128)
			binary.LittleEndian.PutUint64(b[offset+testSHSize+32:], kib-64)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := bytes.Clone(data)
			tc.edit(b)
			_, err := sectionChunks(b)
			if tc.bad {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
	noSections := bytes.Clone(data)
	binary.LittleEndian.PutUint64(noSections[elfSectionOffsetField:], 0)
	binary.LittleEndian.PutUint16(noSections[elfSectionCountField:], 0)
	chunks, err := sectionChunks(noSections)
	require.NoError(t, err)
	require.Equal(t, []extent{{0, len(noSections)}}, chunks)
	extended := bytes.Clone(data)
	binary.LittleEndian.PutUint16(extended[elfSectionCountField:], 0)
	binary.LittleEndian.PutUint64(extended[offset+sectionSizeOffset:], testSHCount)
	_, err = sectionChunks(extended)
	require.NoError(t, err)
	big := make([]byte, 64)
	copy(big, []byte("\x7fELF\x02\x02\x01"))
	binary.BigEndian.PutUint16(big[52:], 64)
	_, err = sectionChunks(big)
	require.ErrorContains(t, err, "little-endian")
	require.Error(t, validateELF(big, "amd64"))
}
