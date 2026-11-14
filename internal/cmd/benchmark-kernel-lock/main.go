// Command benchmark-kernel-lock authenticates the benchmark guest kernel packages.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/kernelsetup"
)

func verifySignature(file string) ([]byte, error) {
	const timeout = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// #nosec G204 -- fixed verifier/keyring; file is the operator-selected output directory's InRelease file.
	cmd := exec.CommandContext(ctx, "gpgv", "--keyring", "/usr/share/keyrings/ubuntu-archive-keyring.gpg", "--output", "-", "--", file)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

func run(args []string) error {
	const lock = "benchmarks/kernel.lock.json"
	switch {
	case len(args) == 1 && args[0] != "--check":
		return kernelsetup.Run(lock, args[0], kernelsetup.Client(), verifySignature)
	case len(args) == 2 && args[0] == "--check":
		return kernelsetup.Check(lock, args[1], kernelsetup.Client(), verifySignature, os.Stdout)
	default:
		return errors.New("usage: benchmark-kernel-lock [--check] OUTPUT_DIRECTORY")
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
