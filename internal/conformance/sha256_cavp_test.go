package conformance_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const monteConcatBlocks = 3

type cavpVectorCase struct {
	Name   string `json:"name"`
	MsgHex string `json:"msg_hex"`
	SHA256 string `json:"sha256"`
}

type cavpMillionVector struct {
	Name    string `json:"name"`
	ByteHex string `json:"byte_hex"`
	Count   int    `json:"count"`
	SHA256  string `json:"sha256"`
}

type cavpMonteCheckpoint struct {
	Round  int    `json:"round"`
	SHA256 string `json:"sha256"`
}

type cavpMonteCarlo struct {
	SeedHex         string                `json:"seed_hex"`
	Rounds          int                   `json:"rounds"`
	InnerIterations int                   `json:"inner_iterations"`
	Checkpoints     []cavpMonteCheckpoint `json:"checkpoints"`
}

type sha256CAVPSuite struct {
	FIPS1804Vectors     []cavpVectorCase  `json:"fips_180_4_vectors"`
	CAVPBoundaryVectors []cavpVectorCase  `json:"cavp_boundary_vectors"`
	MillionAVector      cavpMillionVector `json:"million_a_vector"`
	SHAVSMonteCarlo     cavpMonteCarlo    `json:"shavs_monte_carlo"`
}

func TestNISTCAVPSHA256Conformance(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "formal", "conformance", "sha256_cavp.json"))
	require.NoError(t, err)

	var suite sha256CAVPSuite
	require.NoError(t, json.Unmarshal(raw, &suite))
	require.NotEmpty(t, suite.FIPS1804Vectors)
	require.NotEmpty(t, suite.CAVPBoundaryVectors)

	chunkSteps := [...]int{1, 3, 7, 13, 31, 63, 64, 65, 129}
	verifyStreaming := func(t *testing.T, msg []byte, wantHex string) {
		t.Helper()
		oneshot := sha256.Sum256(msg)
		assert.Equal(t, wantHex, hex.EncodeToString(oneshot[:]))

		h := sha256.New()
		stepIdx := 0
		for off := 0; off < len(msg); {
			take := min(chunkSteps[stepIdx%len(chunkSteps)], len(msg)-off)
			_, writeErr := h.Write(msg[off : off+take])
			require.NoError(t, writeErr)
			off += take
			stepIdx++
		}
		assert.Equal(t, wantHex, hex.EncodeToString(h.Sum(nil)))
	}

	allVectors := append([]cavpVectorCase(nil), suite.FIPS1804Vectors...)
	allVectors = append(allVectors, suite.CAVPBoundaryVectors...)
	for _, vec := range allVectors {
		t.Run(vec.Name, func(t *testing.T) {
			t.Parallel()
			msg, decErr := hex.DecodeString(vec.MsgHex)
			require.NoError(t, decErr)
			verifyStreaming(t, msg, vec.SHA256)
		})
	}

	t.Run(suite.MillionAVector.Name, func(t *testing.T) {
		t.Parallel()
		fill, decErr := hex.DecodeString(suite.MillionAVector.ByteHex)
		require.NoError(t, decErr)
		require.Len(t, fill, 1)
		payload := make([]byte, suite.MillionAVector.Count)
		for i := range payload {
			payload[i] = fill[0]
		}
		verifyStreaming(t, payload, suite.MillionAVector.SHA256)
	})

	t.Run("shavs_monte_carlo", func(t *testing.T) {
		t.Parallel()
		seedBytes, decErr := hex.DecodeString(suite.SHAVSMonteCarlo.SeedHex)
		require.NoError(t, decErr)
		require.Len(t, seedBytes, sha256.Size)

		wantCheckpoints := make(map[int]string, len(suite.SHAVSMonteCarlo.Checkpoints))
		for _, cp := range suite.SHAVSMonteCarlo.Checkpoints {
			wantCheckpoints[cp.Round] = cp.SHA256
		}
		require.NotEmpty(t, wantCheckpoints)

		var curSeed [sha256.Size]byte
		copy(curSeed[:], seedBytes)

		var buf [sha256.Size * monteConcatBlocks]byte
		verified := 0
		for round := 0; round < suite.SHAVSMonteCarlo.Rounds; round++ {
			md0, md1, md2 := curSeed, curSeed, curSeed
			for iter := 0; iter < suite.SHAVSMonteCarlo.InnerIterations; iter++ {
				copy(buf[0:sha256.Size], md0[:])
				copy(buf[sha256.Size:sha256.Size*2], md1[:])
				copy(buf[sha256.Size*2:sha256.Size*monteConcatBlocks], md2[:])
				next := sha256.Sum256(buf[:])
				md0, md1, md2 = md1, md2, next
			}
			curSeed = md2
			if wantHex, ok := wantCheckpoints[round]; ok {
				assert.Equal(t, wantHex, hex.EncodeToString(curSeed[:]), "round %d mismatch", round)
				verified++
			}
		}
		assert.Equal(t, len(wantCheckpoints), verified)
	})
}
