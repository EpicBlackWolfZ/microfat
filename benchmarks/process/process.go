// Package process owns bounded subprocess output, cancellation and process-group cleanup.
package process

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"
)

const (
	MaxOutput = 16 * 1024 * 1024
	WaitDelay = 2 * time.Second
)

var ErrOutputLimit = errors.New("child output exceeded limit")

type Spec struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

type Buffer struct {
	mu       sync.Mutex
	data     bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (b *Buffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(data) > b.limit-b.data.Len() {
		b.overflow = true
		b.cancel()
		return 0, ErrOutputLimit
	}
	return b.data.Write(data)
}

func (b *Buffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.data.Bytes())
}

func (b *Buffer) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.overflow {
		return ErrOutputLimit
	}
	return nil
}

type Child struct {
	cmd    *exec.Cmd
	stdout *Buffer
	stderr *Buffer
	done   chan struct{}
	err    error
	cancel context.CancelFunc
}

func Start(ctx context.Context, spec Spec) (*Child, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...) // #nosec G204 -- explicitly selected benchmark executable.
	cmd.Dir, cmd.Env, cmd.WaitDelay = spec.Dir, spec.Env, WaitDelay
	configureGroup(cmd)
	cmd.Cancel = func() error { return signalGroup(cmd.Process, true) }
	out := &Buffer{limit: MaxOutput, cancel: cancel}
	errOut := &Buffer{limit: MaxOutput, cancel: cancel}
	cmd.Stdout, cmd.Stderr = out, errOut
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	child := &Child{cmd: cmd, stdout: out, stderr: errOut, done: make(chan struct{}), cancel: cancel}
	go func() {
		waitErr := cmd.Wait()
		cleanupErr := signalGroup(cmd.Process, true)
		if errors.Is(cleanupErr, os.ErrProcessDone) {
			cleanupErr = nil
		}
		child.err = errors.Join(waitErr, cleanupErr, out.Err(), errOut.Err())
		cancel()
		close(child.done)
	}()
	return child, nil
}

func (c *Child) PID() int                { return c.cmd.Process.Pid }
func (c *Child) Stdout() []byte          { return c.stdout.Bytes() }
func (c *Child) Stderr() []byte          { return c.stderr.Bytes() }
func (c *Child) Done() <-chan struct{}   { return c.done }
func (c *Child) Wait() error             { <-c.done; return c.err }
func (c *Child) State() *os.ProcessState { <-c.done; return c.cmd.ProcessState }

func (c *Child) Stop(ctx context.Context) error {
	select {
	case <-c.done:
		return c.err
	default:
	}
	err := signalGroup(c.cmd.Process, false)
	if errors.Is(err, os.ErrProcessDone) {
		err = nil
	}
	select {
	case <-c.done:
		return errors.Join(err, c.err)
	case <-ctx.Done():
		c.cancel()
		return errors.Join(err, ctx.Err(), c.Wait())
	}
}

func Run(ctx context.Context, spec Spec) ([]byte, []byte, error) {
	child, err := Start(ctx, spec)
	if err != nil {
		return nil, nil, err
	}
	err = child.Wait()
	return child.Stdout(), child.Stderr(), err
}
