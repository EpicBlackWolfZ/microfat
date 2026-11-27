// Command doc-gate validates repository documentation, ADRs, SPECs, and verification ledgers.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/EpicBlackWolfZ/microfat/internal/docgate"
)

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doc-gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rootDir := fs.String("root", ".", "Repository root directory")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: doc-gate [--root DIR]")
		return 1
	}

	report, err := docgate.Check(*rootDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "doc-gate error: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprint(stdout, report.Format())
	if !report.OK() {
		return 1
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
