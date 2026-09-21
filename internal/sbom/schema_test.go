package sbom

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return data
}

func jsonObject(t *testing.T, data []byte) Object {
	t.Helper()
	var value Object
	require.NoError(t, json.Unmarshal(data, &value))
	return value
}

func jsonBytes(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}

func TestPinnedSchemas(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		spdx bool
	}{
		{"archive.cdx.json", false}, {"converted.spdx.json", true}, {"archive.spdx.json", true},
	} {
		t.Run(test.name, func(t *testing.T) { t.Parallel(); require.NoError(t, ValidateSchema(fixture(t, test.name), test.spdx)) })
	}
}

func TestRejectAmbiguousAndUnboundedJSON(t *testing.T) {
	t.Parallel()
	for name, data := range map[string][]byte{
		"malformed":        []byte(`{"bomFormat":`),
		"duplicate":        []byte(`{"bomFormat":"CycloneDX","bomFormat":"changed"}`),
		"trailing":         []byte(`{} {}`),
		"invalid_utf8":     {'"', 0xff, '"'},
		"depth":            []byte(strings.Repeat("[", maxJSONDepth+2) + "null" + strings.Repeat("]", maxJSONDepth+2)),
		"size":             make([]byte, MaxDocumentBytes+1),
		"invalid_schema":   []byte(`{"bomFormat":"wrong"}`),
		"invalid_object":   []byte(`{"a" 1}`),
		"unfinished_array": []byte(`[1`),
	} {
		t.Run(name, func(t *testing.T) { t.Parallel(); require.Error(t, ValidateSchema(data, false)) })
	}
	require.NoError(t, checkJSON([]byte(`{"a":[1,true,null,{"b":"x"}]} `)))
}

func TestOfflineSchemaAndRegexp(t *testing.T) {
	t.Parallel()
	_, err := (offlineLoader{}).Load("https://untrusted.invalid/schema.json")
	require.ErrorContains(t, err, "not pinned")
	pattern, err := compilePattern(`^(?!_:).+:.+`)
	require.NoError(t, err)
	assert.Equal(t, `^(?!_:).+:.+`, pattern.String())
	assert.True(t, pattern.MatchString("urn:test:node"))
	assert.False(t, pattern.MatchString("_:blank"))
	_, err = compilePattern("[")
	require.Error(t, err)
}
