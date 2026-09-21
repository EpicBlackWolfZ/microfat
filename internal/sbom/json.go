package sbom

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

const maxJSONDepth = 64

// checkJSON rejects ambiguous duplicate keys before either schema or typed
// decoding, and bounds nesting before the schema library traverses the input.
func checkJSON(data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("SBOM JSON is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := consumeJSON(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("SBOM has trailing JSON data")
	}
	return nil
}

func consumeJSON(decoder *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return fmt.Errorf("SBOM JSON nesting exceeds %d", maxJSONDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		return consumeObject(decoder, depth)
	case '[':
		for decoder.More() {
			if err := consumeJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected SBOM JSON delimiter %q", delim)
	}
}

func consumeObject(decoder *json.Decoder, depth int) error {
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return fmt.Errorf("duplicate or invalid SBOM JSON property %q", key)
		}
		seen[key] = true
		if err := consumeJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}
