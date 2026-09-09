package format

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyJSONSyntaxAndDepth(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		`{"unknown":[}}`, `{"unknown":tru}`, `{"unknown":01}`, `{"unknown":1e}`,
		`{"unknown":[1 2]}`, `{"unknown":{"a":1 "b":2}}`, `{"unknown":[1,]}`,
		`{"unknown":{"a":1,}}`, `{"version":1 "name":"x"}`, `{} {}`,
		"{\"unknown\":\"\x01\"}", `{"unknown":"\x"}`, `{"unknown":`,
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			_, err := unmarshalJSONIndex([]byte(value))
			require.ErrorIs(t, err, ErrInvalidJSONSyntax)
		})
	}
	for _, depth := range []int{MaxJSONDepth - 1, MaxJSONDepth} {
		data := []byte(`{"unknown":` + strings.Repeat("[", depth) + `null` + strings.Repeat("]", depth) + `}`)
		_, err := unmarshalJSONIndex(data)
		if depth == MaxJSONDepth {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
	_, err := unmarshalJSONIndex([]byte(`{"unknown":{"a":[null,true,false,-1.5e+2,"[\\\"]"]},"version":1}`))
	require.NoError(t, err)
	for _, input := range []string{"", "}", "true", "[null]"} {
		next, err := skipJSONValue([]byte(input), 0)
		if input == "" || input == "}" {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			assert.Greater(t, next, 0)
		}
	}
}

func FuzzLegacyJSONSyntax(f *testing.F) {
	for _, seed := range []string{`null`, `{"a":[1,true,"x"]}`, `[}}`, `[1,]`, `{"a":01}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// Length below the ceiling cannot exceed the intentional nesting restriction.
		if len(data) > MaxJSONDepth {
			t.Skip()
		}
		assert.Equal(t, json.Valid(data), validateJSONSyntax(data) == nil)
		manifest := append([]byte(`{"unknown":`), data...)
		manifest = append(manifest, '}')
		_, err := unmarshalJSONIndex(manifest)
		assert.Equal(t, json.Valid(manifest), err == nil)
	})
}
