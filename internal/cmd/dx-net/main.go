// Command dx-net supplies profiling port probes and temporary test servers.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/dxnet"
)

const serverLifetime = 5 * time.Minute

func run(ctx context.Context, args []string, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: dx-net probe-port PORT|serve")
	}
	switch args[0] {
	case "probe-port":
		const argumentCount = 2
		if len(args) != argumentCount {
			return errors.New("probe-port requires one port")
		}
		occupied, err := dxnet.Occupied(ctx, args[1])
		if err != nil {
			return err
		}
		return writeState(out, occupied)
	case "serve":
		if len(args) != 1 {
			return errors.New("serve takes no arguments")
		}
		var config net.ListenConfig
		listener, err := config.Listen(ctx, "tcp4", "127.0.0.1:0")
		if err != nil {
			return err
		}
		bounded, cancel := context.WithTimeout(ctx, serverLifetime)
		defer cancel()
		return dxnet.Serve(bounded, listener, out)
	default:
		return errors.New("unknown dx-net command")
	}
}
func writeState(out io.Writer, occupied bool) error {
	state := "free"
	if occupied {
		state = "occupied"
	}
	_, err := fmt.Fprintln(out, state)
	return err
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
