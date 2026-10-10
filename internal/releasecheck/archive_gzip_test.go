package releasecheck_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	gzipTestPaddingBytes     = 64 * 1024
	gzipTestMaxPaddingBytes  = 1024 * 1024
	gzipTestFooterBytes      = 8
	gzipTestSizeBytes        = 4
	gzipTestHeaderPrefixSize = 3
)

type gzipArchiveCase struct {
	name        string
	data        []byte
	wantError   bool
	wantErrIs   error
	wantMessage string
}

func gzipTestMember(t *testing.T, data []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return compressed.Bytes()
}

func gzipTestCorruptFooter(data []byte, offsetFromEnd int) []byte {
	corrupted := bytes.Clone(data)
	corrupted[len(corrupted)-offsetFromEnd] ^= 1
	return corrupted
}

func gzipTestAppend(base, suffix []byte) []byte {
	return append(bytes.Clone(base), suffix...)
}

func gzipTestTarData(t *testing.T, contract *releasecheck.ReleaseContract) []byte {
	t.Helper()
	archivePath := filepath.Join(t.TempDir(), "microfat_0.3.0_linux_amd64.tar.gz")
	createOrderedTarGz(t, archivePath, createStandardValidEntries(t, releasecheck.ArchAMD64, contract))
	compressed, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	return data
}

func TestArchiveGzipIntegrity(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.3.0")
	require.NoError(t, err)
	tarData := gzipTestTarData(t, contract)
	padding := make([]byte, gzipTestPaddingBytes)
	unpaddedArchive := gzipTestMember(t, tarData)
	paddedArchive := gzipTestMember(t, gzipTestAppend(tarData, padding))
	emptyMember := gzipTestMember(t, nil)
	zeroMember := gzipTestMember(t, padding)
	nonzeroPadding := bytes.Clone(padding)
	nonzeroPadding[len(nonzeroPadding)-1] = 1

	cases := []gzipArchiveCase{
		{name: "valid_no_padding", data: unpaddedArchive},
		{name: "valid_zero_padding", data: paddedArchive},
		{name: "valid_empty_member", data: gzipTestAppend(paddedArchive, emptyMember)},
		{name: "valid_zero_member", data: gzipTestAppend(paddedArchive, zeroMember)},
		{
			name: "valid_padding_limit",
			data: gzipTestMember(t, gzipTestAppend(tarData, make([]byte, gzipTestMaxPaddingBytes))),
		},
		{
			name: "valid_padding_limit_across_members",
			data: gzipTestAppend(paddedArchive, gzipTestMember(t, make([]byte, gzipTestMaxPaddingBytes-gzipTestPaddingBytes))),
		},
		{
			name:      "corrupted_crc",
			data:      gzipTestCorruptFooter(paddedArchive, gzipTestFooterBytes),
			wantError: true,
			wantErrIs: gzip.ErrChecksum,
		},
		{
			name:      "corrupted_isize",
			data:      gzipTestCorruptFooter(paddedArchive, gzipTestSizeBytes),
			wantError: true,
			wantErrIs: gzip.ErrChecksum,
		},
		{
			name:      "missing_footer",
			data:      paddedArchive[:len(paddedArchive)-gzipTestFooterBytes],
			wantError: true,
			wantErrIs: io.ErrUnexpectedEOF,
		},
		{
			name:      "partial_footer",
			data:      paddedArchive[:len(paddedArchive)-gzipTestSizeBytes],
			wantError: true,
			wantErrIs: io.ErrUnexpectedEOF,
		},
		{
			name:      "corrupted_crc_no_padding",
			data:      gzipTestCorruptFooter(unpaddedArchive, gzipTestFooterBytes),
			wantError: true,
			wantErrIs: gzip.ErrChecksum,
		},
		{
			name:      "corrupted_isize_no_padding",
			data:      gzipTestCorruptFooter(unpaddedArchive, gzipTestSizeBytes),
			wantError: true,
			wantErrIs: gzip.ErrChecksum,
		},
		{
			name:      "missing_footer_no_padding",
			data:      unpaddedArchive[:len(unpaddedArchive)-gzipTestFooterBytes],
			wantError: true,
			wantErrIs: io.ErrUnexpectedEOF,
		},
		{
			name:      "partial_footer_no_padding",
			data:      unpaddedArchive[:len(unpaddedArchive)-gzipTestSizeBytes],
			wantError: true,
			wantErrIs: io.ErrUnexpectedEOF,
		},
		{
			name:        "nonzero_padding",
			data:        gzipTestMember(t, gzipTestAppend(tarData, nonzeroPadding)),
			wantError:   true,
			wantMessage: "unexpected data after tar archive",
		},
		{
			name:        "nonzero_member",
			data:        gzipTestAppend(paddedArchive, gzipTestMember(t, nonzeroPadding)),
			wantError:   true,
			wantMessage: "unexpected data after tar archive",
		},
		{
			name:        "second_complete_tar_member",
			data:        gzipTestAppend(paddedArchive, unpaddedArchive),
			wantError:   true,
			wantMessage: "unexpected data after tar archive",
		},
		{
			name:      "next_member_corrupted_crc",
			data:      gzipTestAppend(paddedArchive, gzipTestCorruptFooter(zeroMember, gzipTestFooterBytes)),
			wantError: true,
			wantErrIs: gzip.ErrChecksum,
		},
		{
			name:      "next_member_corrupted_isize",
			data:      gzipTestAppend(paddedArchive, gzipTestCorruptFooter(zeroMember, gzipTestSizeBytes)),
			wantError: true,
			wantErrIs: gzip.ErrChecksum,
		},
		{
			name:      "next_member_missing_footer",
			data:      gzipTestAppend(paddedArchive, zeroMember[:len(zeroMember)-gzipTestFooterBytes]),
			wantError: true,
			wantErrIs: io.ErrUnexpectedEOF,
		},
		{
			name:      "next_member_partial_footer",
			data:      gzipTestAppend(paddedArchive, zeroMember[:len(zeroMember)-gzipTestSizeBytes]),
			wantError: true,
			wantErrIs: io.ErrUnexpectedEOF,
		},
		{
			name:      "malformed_next_member",
			data:      gzipTestAppend(paddedArchive, emptyMember[:gzipTestHeaderPrefixSize]),
			wantError: true,
			wantErrIs: io.ErrUnexpectedEOF,
		},
		{
			name:      "raw_trailing_garbage",
			data:      gzipTestAppend(paddedArchive, []byte("not a gzip member")),
			wantError: true,
			wantErrIs: gzip.ErrHeader,
		},
		{
			name:      "short_raw_trailing_garbage",
			data:      gzipTestAppend(paddedArchive, []byte("garbage")),
			wantError: true,
			wantErrIs: io.ErrUnexpectedEOF,
		},
		{
			name:      "raw_trailing_zero",
			data:      gzipTestAppend(paddedArchive, []byte{0}),
			wantError: true,
			wantErrIs: io.ErrUnexpectedEOF,
		},
		{
			name:        "padding_limit_exceeded",
			data:        gzipTestMember(t, gzipTestAppend(tarData, make([]byte, gzipTestMaxPaddingBytes+1))),
			wantError:   true,
			wantMessage: "padding limit",
		},
		{
			name:        "padding_limit_exceeded_across_members",
			data:        gzipTestAppend(paddedArchive, gzipTestMember(t, make([]byte, gzipTestMaxPaddingBytes-gzipTestPaddingBytes+1))),
			wantError:   true,
			wantMessage: "padding limit",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			archivePath := filepath.Join(t.TempDir(), "microfat_0.3.0_linux_amd64.tar.gz")
			require.NoError(t, os.WriteFile(archivePath, tc.data, 0o644))
			checkGzipArchiveAPIs(t, archivePath, contract, tc)
		})
	}
}

func checkGzipArchiveAPIs(t *testing.T, archivePath string, contract *releasecheck.ReleaseContract, tc gzipArchiveCase) {
	t.Helper()
	t.Run("ValidateArchive", func(t *testing.T) {
		t.Parallel()
		facts, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
		if facts != nil {
			t.Cleanup(func() { require.NoError(t, facts.Cleanup()) })
		}
		if tc.wantError {
			checkGzipArchiveError(t, err, tc)
			assert.Nil(t, facts)
			return
		}
		require.NoError(t, err)
		require.NotNil(t, facts)
		expectedSHA := sha256.Sum256(tc.data)
		assert.Equal(t, hex.EncodeToString(expectedSHA[:]), facts.ArchiveSHA256)
	})
	t.Run("ExtractArchiveSafely", func(t *testing.T) {
		t.Parallel()
		targetDir := t.TempDir()
		err := releasecheck.ExtractArchiveSafely(archivePath, targetDir)
		if tc.wantError {
			checkGzipArchiveError(t, err, tc)
			return
		}
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(targetDir, testReadme))
		require.NoError(t, err)
		assert.Equal(t, []byte("# README"), data)
	})
	t.Run("ExtractFileFromArchive", func(t *testing.T) {
		t.Parallel()
		destPath := filepath.Join(t.TempDir(), "existing-destination")
		original := []byte("preserve existing destination on validation failure")
		require.NoError(t, os.WriteFile(destPath, original, 0o644))
		err := releasecheck.ExtractFileFromArchive(archivePath, testReadme, destPath)
		if tc.wantError {
			checkGzipArchiveError(t, err, tc)
		} else {
			require.NoError(t, err)
		}
		data, readErr := os.ReadFile(destPath)
		require.NoError(t, readErr)
		if tc.wantError {
			assert.Equal(t, original, data)
		} else {
			assert.Equal(t, []byte("# README"), data)
		}
	})
}

func checkGzipArchiveError(t *testing.T, err error, tc gzipArchiveCase) {
	t.Helper()
	if tc.wantErrIs != nil {
		assert.ErrorIs(t, err, tc.wantErrIs)
	} else {
		assert.ErrorContains(t, err, tc.wantMessage)
	}
}
