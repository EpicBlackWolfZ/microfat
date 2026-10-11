// Command benchmark-cleanup authenticates and drains private benchmark delegations.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/benchmarkcleanup"
)

func main() {
	const cleanupTimeout = 15 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	err := benchmarkcleanup.Run(ctx, os.Args[1:])
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
