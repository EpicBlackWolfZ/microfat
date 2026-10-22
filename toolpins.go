package microfat

import (
	_ "embed"
	"strings"
)

//go:embed .config/tool-versions.env
var toolDefinitions string

// ToolPin returns a tool's version or version@SHA-256 pair from the shared pins.
// Unknown names return an empty string. The embedded file is also used by CI.
func ToolPin(name string) string {
	for line := range strings.Lines(toolDefinitions) {
		key, value, ok := strings.Cut(line, "=")
		if ok && key == name {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
