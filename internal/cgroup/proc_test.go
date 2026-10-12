package cgroup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanProcCgroupLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input, token string
		eof                bool
		advance            int
	}{
		{name: "newline preserves carriage return", input: "0::/app\r\nnext", token: "0::/app\r", advance: len("0::/app\r\n")},
		{name: "EOF preserves carriage return", input: "0::/app\r", token: "0::/app\r", eof: true, advance: len("0::/app\r")},
		{name: "needs more data", input: "0::/app\r"},
		{name: "empty EOF", eof: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			advance, token, err := ScanProcCgroupLine([]byte(tc.input), tc.eof)
			require.NoError(t, err)
			assert.Equal(t, tc.advance, advance)
			assert.Equal(t, tc.token, string(token))
		})
	}
}
