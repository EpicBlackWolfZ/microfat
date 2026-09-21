// Package sbom implements pinned, offline modern SBOM structure validation.
package sbom

import (
	"bytes"
	"embed"
	"fmt"
	"sync"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	// MaxDocumentBytes bounds a release SBOM before schema traversal.
	MaxDocumentBytes = 32 << 20
	CycloneDXSchema  = "https://cyclonedx.org/schema/bom-1.7.schema.json"
	SPDXSchema       = "https://spdx.org/schema/3.0.1/spdx-json-schema.json"
	SPDXContext      = "https://spdx.org/rdf/3.0.1/spdx-context.jsonld"
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
	compiled, err := regexp2.Compile(value, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	compiled.MatchTimeout = time.Second
	return ecmaPattern{compiled}, nil
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
