package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const minExecChildArgs = 3

// Variant is injected at build time via -ldflags="-X main.Variant=...".
var Variant = "unknown"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--exit-code":
			if len(os.Args) < 3 {
				os.Exit(1)
			}
			code, err := strconv.Atoi(os.Args[2])
			if err != nil {
				os.Exit(1)
			}
			os.Exit(code)
		case "--echo-args":
			fmt.Print(strings.Join(os.Args[2:], "||"))
			return
		case "--echo-env":
			if len(os.Args) < 3 {
				os.Exit(1)
			}
			fmt.Print(os.Getenv(os.Args[2]))
			return
		case "--cat-stdin":
			_, _ = io.Copy(os.Stdout, os.Stdin)
			return
		case "--exec-child":
			if len(os.Args) < minExecChildArgs {
				os.Exit(1)
			}
			// #nosec G204 G702 -- test fixture helper executes caller-specified child test binary.
			cmd := exec.Command(os.Args[2], os.Args[minExecChildArgs:]...)
			cmd.Env = os.Environ()
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					os.Exit(exitErr.ExitCode())
				}
				fmt.Fprintf(os.Stderr, "exec-child error: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	fmt.Printf("golden:variant=%s\n", Variant)
	if v := os.Getenv("TEST_CUSTOM_KEY"); v != "" {
		fmt.Printf("golden:env=%s\n", v)
	}
}
