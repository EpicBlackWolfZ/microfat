package main

import (
	"fmt"
	"os"
)

const usageExit = 2

func main() {
	if len(os.Args) != 2 || os.Args[1] != "--startup-only" {
		os.Exit(usageExit)
	}
	fmt.Print("MICROFAT_TELEMETRY_STARTUP_READY_v1\n")
}
