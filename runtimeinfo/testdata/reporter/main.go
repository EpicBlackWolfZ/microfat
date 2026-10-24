// reporter exposes runtimeinfo from a native or packed process to black-box tests.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/runtimeinfo"
)

const unsetGCPercent = -2

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("runtimeinfo-reporter", flag.ContinueOnError)
	var environment []string
	flags.Func("set-env", "change KEY=VALUE after runtime startup (repeatable)", func(value string) error {
		environment = append(environment, value)
		return nil
	})
	memoryLimit := flags.Int64("memory-limit", -1, "set the running Go memory limit in bytes")
	maxProcs := flags.Int("maxprocs", 0, "set the running Go scheduler parallelism")
	gcPercent := flags.Int("gc-percent", unsetGCPercent, "set the Go GC percentage (-1 disables its percentage trigger)")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	for _, value := range environment {
		key, raw, found := strings.Cut(value, "=")
		if !found {
			return fmt.Errorf("set-env requires KEY=VALUE, got %q", value)
		}
		if err := os.Setenv(key, raw); err != nil {
			return err
		}
	}
	if *memoryLimit >= 0 {
		debug.SetMemoryLimit(*memoryLimit)
	}
	if *maxProcs > 0 {
		runtime.GOMAXPROCS(*maxProcs)
	}
	if *gcPercent != unsetGCPercent {
		debug.SetGCPercent(*gcPercent)
	}
	return json.NewEncoder(os.Stdout).Encode(runtimeinfo.Read())
}
