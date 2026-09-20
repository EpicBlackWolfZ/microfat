package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/runtimeinit"
)

func main() {
	dir := os.Getenv("APP_ASSET_DIR")
	if dir == "" {
		exe, err := runtimeinit.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		dir = filepath.Dir(exe)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(string(data))
}
