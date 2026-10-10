package releasecheck

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

func TestArchiveTrailerDecodedBudget(t *testing.T) {
	t.Parallel()
	const availableBytes = 3
	cases := []struct {
		name      string
		reader    io.Reader
		remaining int64
		wantError string
	}{
		{
			name: "real_eof_before_budget", reader: bytes.NewReader(make([]byte, availableBytes)),
			remaining: availableBytes + 1,
		},
		{
			name: "real_eof_with_final_bytes", reader: iotest.DataErrReader(bytes.NewReader(make([]byte, availableBytes))),
			remaining: availableBytes + 1,
		},
		{
			name: "synthetic_eof_before_read", reader: strings.NewReader("unread data"), remaining: 0,
			wantError: "decompressed size limit",
		},
		{
			name: "synthetic_eof_after_read", reader: bytes.NewReader(make([]byte, availableBytes+1)),
			remaining: availableBytes, wantError: "decompressed size limit",
		},
		{
			name: "real_eof_at_exhausted_budget", reader: iotest.DataErrReader(bytes.NewReader(make([]byte, availableBytes))),
			remaining: availableBytes, wantError: "decompressed size limit",
		},
		{
			name: "nonzero_final_bytes", reader: iotest.DataErrReader(strings.NewReader("data")),
			remaining: drainBufferSize, wantError: "unexpected data after tar archive",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := drainArchiveTrailer(&io.LimitedReader{R: tc.reader, N: tc.remaining})
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
