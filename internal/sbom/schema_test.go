package sbom

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	concurrentSchemaWorkers = 16
	concurrentSchemaRounds  = 4
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

	t.Run("re2_fast_path", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name    string
			pattern string
			good    string
			bad     string
		}{
			{
				name:    "uuid_urn",
				pattern: `^urn:uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
				good:    "urn:uuid:3e671687-395b-41f5-a30f-a58921a69b79",
				bad:     "urn:uuid:not-a-uuid",
			},
			{
				name:    "non_capturing_digits_and_escaped_dot",
				pattern: `^(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)$`,
				good:    "1.27",
				bad:     "01.2",
			},
			{
				name:    "literal_dot_in_class_and_negated_class",
				pattern: `^[a-z.]+:[^:]+$`,
				good:    "pkg.go:microfat",
				bad:     "pkg:a:b",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				compiled, compileErr := compilePattern(tc.pattern)
				require.NoError(t, compileErr)
				_, isRE2 := compiled.(*regexp.Regexp)
				assert.True(t, isRE2, "expected stdlib RE2 engine for %s", tc.pattern)
				assert.Equal(t, tc.pattern, compiled.String())
				assert.True(t, compiled.MatchString(tc.good))
				assert.False(t, compiled.MatchString(tc.bad))
			})
		}
	})

	t.Run("ecma_fallback_semantics", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name    string
			pattern string
			good    string
			bad     string
		}{
			{
				name:    "negative_lookahead",
				pattern: `^(?!_:).+:.+`,
				good:    "urn:test:node",
				bad:     "_:blank",
			},
			{
				name:    "dot_rejects_carriage_return",
				pattern: `^_:.+$`,
				good:    "_:creation",
				bad:     "_:crea\rtion",
			},
			{
				name:    "ecma_whitespace_matches_nbsp",
				pattern: `^[A-Za-z]+\s+[A-Za-z]+$`,
				good:    "Apache\u00a0License",
				bad:     "ApacheLicense",
			},
			{
				name:    "named_group_modifier",
				pattern: `^(?<tag>[a-z]+)$`,
				good:    "vfour",
				bad:     "V4",
			},
			{
				name:    "negated_empty_class_prefix",
				pattern: `^[^]z$`,
				good:    "az",
				bad:     "z",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				compiled, compileErr := compilePattern(tc.pattern)
				require.NoError(t, compileErr)
				_, isECMA := compiled.(ecmaPattern)
				assert.True(t, isECMA, "expected ECMA engine for %s", tc.pattern)
				assert.Equal(t, tc.pattern, compiled.String())
				assert.True(t, compiled.MatchString(tc.good))
				assert.False(t, compiled.MatchString(tc.bad))
			})
		}
	})

	t.Run("re2_equivalence_classifier_rejects_unsafe_forms", func(t *testing.T) {
		t.Parallel()
		for _, raw := range []string{
			"a\tb",
			`^trailing\`,
			`^[unclosed`,
			`^[]a]$`,
			`^[[:alpha:]]$`,
			`^\w+$`,
			"^(?",
		} {
			assert.False(t, isASCIIRE2Equivalent(raw), "pattern %q must not classify as RE2-equivalent", raw)
		}
	})

	_, err = compilePattern("[")
	require.Error(t, err)
}

func TestValidateSchemaConcurrentWithoutClockGoroutine(t *testing.T) {
	t.Parallel()
	cdxDoc := fixture(t, "archive.cdx.json")
	spdxDoc := fixture(t, "archive.spdx.json")

	var wg sync.WaitGroup
	wg.Add(concurrentSchemaWorkers)
	for range concurrentSchemaWorkers {
		go func() {
			defer wg.Done()
			for range concurrentSchemaRounds {
				assert.NoError(t, ValidateSchema(cdxDoc, false))
				assert.NoError(t, ValidateSchema(spdxDoc, true))
			}
		}()
	}
	wg.Wait()

	var stacks bytes.Buffer
	prof := pprof.Lookup("goroutine")
	require.NotNil(t, prof)
	require.NoError(t, prof.WriteTo(&stacks, 1))
	assert.NotContains(t, stacks.String(), "regexp2.runClock")
}
