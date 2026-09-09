package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
)

func TestMalformedLegacyManifestTerminates(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"inspect", "verify"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			manifest := []byte(`{"version":1,"unknown":[}}`)
			trailer := make([]byte, format.TrailerSize)
			binary.LittleEndian.PutUint64(trailer[format.OffsetLen:], uint64(len(manifest)))
			hash := sha256.Sum256(manifest)
			copy(trailer[format.OffsetLen+format.SizeLen:], hash[:])
			copy(trailer[len(trailer)-format.MagicLen:], format.MagicString)
			file := filepath.Join(t.TempDir(), "malformed.fat")
			require.NoError(t, os.WriteFile(file, append(manifest, trailer...), privateFilePerm))
			const deadline = 5 * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			defer cancel()
			output, err := exec.CommandContext(ctx, cliPath, command, file).CombinedOutput()
			require.Error(t, err, string(output))
			require.NoError(t, ctx.Err(), "malformed manifest command hung")
		})
	}
}
