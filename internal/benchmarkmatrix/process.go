package benchmarkmatrix

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
)

// RunProcess uses the benchmark process owner for bounded output and group cleanup.
// Cancellation first requests a graceful stop so the harness can finish its evidence.
func RunProcess(ctx context.Context, spec process.Spec, stdout, stderr io.Writer) (int, error) {
	if err := ctx.Err(); err != nil {
		return 1, err
	}
	child, err := process.Start(context.Background(), spec)
	if err != nil {
		return 1, err
	}
	select {
	case <-child.Done():
		err = child.Wait()
	case <-ctx.Done():
		const grace = 30 * time.Second
		stop, cancel := context.WithTimeout(context.Background(), grace)
		err = errors.Join(ctx.Err(), child.Stop(stop))
		cancel()
	}
	_, outErr := stdout.Write(child.Stdout())
	_, errErr := stderr.Write(child.Stderr())
	return child.State().ExitCode(), errors.Join(err, outErr, errErr)
}
