package main

import (
	"fmt"
	"os"

	"github.com/EpicBlackWolfZ/microfat/runtimeinit"
)

func main() {
	exe, err := runtimeinit.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(exe)
}
