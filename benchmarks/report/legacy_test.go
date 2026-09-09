package report

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyEvidenceCompatibility(t *testing.T) {
	t.Parallel()
	const fixture = "testdata/legacy-v1.json"
	exp, hashed, err := ReadLegacy(fixture)
	require.NoError(t, err)
	assert.False(t, hashed)
	evidence, err := schema.BuildEvidence(exp)
	require.NoError(t, err)
	data, err := schema.SerializeEvidence(evidence)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "v1.json")
	require.NoError(t, os.WriteFile(path, data, fileMode))
	read, hashed, err := ReadLegacy(path)
	require.NoError(t, err)
	assert.True(t, hashed)
	assert.Equal(t, exp.ID, read.ID)
	for _, input := range []string{path, fixture} {
		message, err := VerifyInput(input)
		require.NoError(t, err)
		assert.NotEmpty(t, message)
		for _, format := range []string{"terminal", "markdown", "json"} {
			rendered, err := RenderInput(input, format)
			require.NoError(t, err)
			assert.Contains(t, string(rendered), exp.ID)
		}
	}
	_, err = RenderInput(path, "invalid")
	require.Error(t, err)
	evidence.DigestSHA256 = "corrupt"
	data, err = schema.SerializeEvidence(evidence)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, fileMode))
	_, _, err = ReadLegacy(path)
	require.Error(t, err)
	for _, input := range []string{"/missing", path} {
		_, err := VerifyInput(input)
		require.Error(t, err)
		_, err = RenderInput(input, "json")
		require.Error(t, err)
	}
	for _, invalid := range []string{"{", "{}", `{"payload_bytes":"eA==","schema_version":"future"}`} {
		require.NoError(t, os.WriteFile(path, []byte(invalid), fileMode))
		_, _, err := ReadLegacy(path)
		require.Error(t, err)
	}
}

func TestLegacyEnvelopeInvalidPayload(t *testing.T) {
	t.Parallel()
	payload := []byte("{")
	envelope := &schema.Evidence{SchemaVersion: schema.SchemaVersion, PayloadBytes: payload, DigestSHA256: schema.Digest(payload)}
	data, err := schema.SerializeEvidence(envelope)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "invalid.json")
	require.NoError(t, os.WriteFile(path, data, fileMode))
	_, _, err = ReadLegacy(path)
	require.Error(t, err)
}
