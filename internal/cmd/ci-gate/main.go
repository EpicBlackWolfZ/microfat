// Command ci-gate validates GitHub's complete prerequisite result set from stdin.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/EpicBlackWolfZ/microfat/internal/cigate"
)

func run(args []string, input io.Reader, output io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(output, "usage: ci-gate EVENT < needs.json")
		return 1
	}
	data, err := io.ReadAll(io.LimitReader(input, cigate.MaxNeedsBytes+1))
	if err == nil {
		err = cigate.Evaluate(data, args[0])
	}
	if err != nil {
		_, _ = fmt.Fprintln(output, err)
		return 1
	}
	_, _ = fmt.Fprintln(output, "All required CI stages satisfied their result contract.")
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout)) }
