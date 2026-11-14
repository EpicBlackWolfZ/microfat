// Package sbom implements pinned, offline modern SBOM structure validation.
package sbom

import (
	"bytes"
	"embed"
	"fmt"
	"regexp"
	"sync"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	// MaxDocumentBytes bounds a release SBOM before schema traversal.
	MaxDocumentBytes = 32 << 20
	CycloneDXSchema  = "https://cyclonedx.org/schema/bom-1.7.schema.json"
	SPDXSchema       = "https://spdx.org/schema/3.0.1/spdx-json-schema.json"
	SPDXContext      = "https://spdx.org/rdf/3.0.1/spdx-context.jsonld"

	minPrintableASCII = 0x20
	maxPrintableASCII = 0x7e
)

//go:embed schema/*.json
var schemaFiles embed.FS

var schemas = sync.OnceValues(compileSchemas)

type schemaSet struct {
	cdx, spdx *jsonschema.Schema
}

type offlineLoader struct{}

func (offlineLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("SBOM schema reference is not pinned: %s", url)
}

type ecmaPattern struct{ compiled *regexp2.Regexp }

func (p ecmaPattern) MatchString(value string) bool {
	matched, err := p.compiled.MatchString(value)
	return err == nil && matched
}

func (p ecmaPattern) String() string { return p.compiled.String() }

func compilePattern(value string) (jsonschema.Regexp, error) {
	// Only pinned embedded schemas are ever compiled (offlineLoader rejects external URIs)
	// and documents are pre-bounded by MaxDocumentBytes. Do not set regexp2.MatchTimeout:
	// regexp2's global fastclock goroutine returns false-negative timeouts when 100ms sleeps
	// overshoot under heavy CPU load and lingers after validation returns.
	compiled, err := regexp2.Compile(value, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	if isASCIIRE2Equivalent(value) {
		if re, reErr := regexp.Compile(value); reErr == nil {
			return re, nil
		}
	}
	return ecmaPattern{compiled}, nil
}

// isASCIIRE2Equivalent reports whether an ECMA-262 pattern uses only constructs whose
// matching semantics are identical in Go's linear-time RE2 engine (no unescaped '.',
// lookarounds, inline modifiers, empty/leading-']' classes, Unicode whitespace/word
// classes, or nested POSIX bracket expressions).
func isASCIIRE2Equivalent(pattern string) bool {
	inClass := false
	classLead := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c < minPrintableASCII || c > maxPrintableASCII {
			return false
		}
		if c == '\\' {
			i++
			if i >= len(pattern) || !isSafeRE2Escape(pattern[i]) {
				return false
			}
			classLead = false
			continue
		}
		if inClass {
			var ok bool
			inClass, classLead, ok = stepCharClass(c, classLead)
			if !ok {
				return false
			}
			continue
		}
		if !stepOutsideClass(pattern, i, c) {
			return false
		}
		if c == '[' {
			inClass = true
			classLead = true
		}
	}
	return !inClass
}

func stepCharClass(c byte, classLead bool) (inClass, nextLead, ok bool) {
	if c == '[' {
		return true, false, false
	}
	if classLead && c == '^' {
		return true, true, true
	}
	if c == ']' {
		if classLead {
			return true, false, false
		}
		return false, false, true
	}
	return true, false, true
}

func stepOutsideClass(pattern string, idx int, c byte) bool {
	if c == '.' {
		return false
	}
	if c == '(' && idx+1 < len(pattern) && pattern[idx+1] == '?' {
		return idx+2 < len(pattern) && pattern[idx+2] == ':'
	}
	return true
}

func isSafeRE2Escape(c byte) bool {
	switch c {
	case 'd', '.', '/', '-', '+', '*', '?', '(', ')', '[', ']', '{', '}', '^', '$', '|', '\\':
		return true
	default:
		return false
	}
}

func compileSchemas() (schemaSet, error) {
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineLoader{})
	compiler.UseRegexpEngine(compilePattern)
	compiler.AssertFormat()
	resources := map[string]string{
		CycloneDXSchema: "bom-1.7.schema.json",
		"http://cyclonedx.org/schema/bom-1.7.schema.json":            "bom-1.7.schema.json",
		"http://cyclonedx.org/schema/spdx.schema.json":               "spdx.schema.json",
		"http://cyclonedx.org/schema/jsf-0.82.schema.json":           "jsf-0.82.schema.json",
		"https://cyclonedx.org/schema/spdx.schema.json":              "spdx.schema.json",
		"https://cyclonedx.org/schema/jsf-0.82.schema.json":          "jsf-0.82.schema.json",
		"http://cyclonedx.org/schema/cryptography-defs.schema.json":  "cryptography-defs.schema.json",
		"https://cyclonedx.org/schema/cryptography-defs.schema.json": "cryptography-defs.schema.json",
		SPDXSchema: "spdx-3.0.1.schema.json",
	}
	for url, name := range resources {
		data, err := schemaFiles.ReadFile("schema/" + name)
		if err != nil {
			return schemaSet{}, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return schemaSet{}, err
		}
		if err := compiler.AddResource(url, doc); err != nil {
			return schemaSet{}, err
		}
	}
	cdx, err := compiler.Compile(CycloneDXSchema)
	if err != nil {
		return schemaSet{}, err
	}
	spdx, err := compiler.Compile(SPDXSchema)
	return schemaSet{cdx: cdx, spdx: spdx}, err
}

// ValidateSchema validates an entire document against the embedded official schema.
// Unknown references cannot trigger filesystem or network access.
func ValidateSchema(data []byte, spdx bool) error {
	if len(data) > MaxDocumentBytes {
		return fmt.Errorf("SBOM exceeds %d bytes", MaxDocumentBytes)
	}
	if err := checkJSON(data); err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("SBOM JSON: %w", err)
	}
	compiled, err := schemas()
	if err != nil {
		return fmt.Errorf("compiling pinned SBOM schemas: %w", err)
	}
	schema := compiled.cdx
	if spdx {
		schema = compiled.spdx
	}
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("SBOM schema: %w", err)
	}
	return nil
}
