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

func verifySignature(file string) error {
	const timeout = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// #nosec G204 -- fixed verifier/keyring; file is the operator-selected output directory's InRelease file.
	cmd := exec.CommandContext(ctx, "gpgv", "--keyring", "/usr/share/keyrings/ubuntu-archive-keyring.gpg", "--", file)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func run(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: benchmark-kernel-lock OUTPUT_DIRECTORY")
	}
	return kernelsetup.Run("benchmarks/kernel.lock.json", args[0], kernelsetup.Client(), verifySignature)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
