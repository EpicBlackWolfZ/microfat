// Package microfat exposes build requirements shared by the development tools.
package microfat

import (
	_ "embed"
	"strings"
)

//go:embed go.mod
var moduleDefinition string

// GoVersion returns the required Go toolchain version from the embedded go.mod.
// Keeping the requirement in the binary also supports tools run outside a checkout.
func GoVersion() string {
	return goDirective(moduleDefinition)
}

func goDirective(module string) string {
	for line := range strings.Lines(module) {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" {
			return fields[1]
		}
	}
	return ""
}
