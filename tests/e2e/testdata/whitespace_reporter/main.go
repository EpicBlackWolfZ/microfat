package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/runtimeinit"
)

type PayloadReport struct {
	OriginalExeHint string   `json:"original_exe_hint"`
	Executable      string   `json:"executable"`
	Args            []string `json:"args"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--fail" {
		os.Exit(42)
	}

	exe, err := runtimeinit.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtimeinit.Executable error: %v\n", err)
		os.Exit(2)
	}
	rep := PayloadReport{
		OriginalExeHint: os.Getenv(format.EnvOriginalExe),
		Executable:      exe,
		Args:            os.Args[1:],
	}
	if err := json.NewEncoder(os.Stdout).Encode(rep); err != nil {
		fmt.Fprintf(os.Stderr, "json encode error: %v\n", err)
		os.Exit(3)
	}
}
