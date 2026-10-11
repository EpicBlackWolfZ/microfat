// Package benchmarkcleanup drains only authenticated, transient benchmark delegations.
package benchmarkcleanup

import (
	"context"
	"errors"
	"time"
)

const (
	gracePeriod = 3 * time.Second
	forcePeriod = 5 * time.Second
	pollPeriod  = 25 * time.Millisecond
)

type delegation interface {
	populated() (bool, error)
	terminate() error
	force() error
	remove() error
}

func drain(ctx context.Context, tree delegation, grace, force time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	populated, err := tree.populated()
	if err != nil {
		return err
	}
	if populated {
		if err := tree.terminate(); err != nil {
			return err
		}
		if err := waitEmpty(ctx, tree, grace); err != nil {
			if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return err
			}
			if err := tree.force(); err != nil {
				return err
			}
			if err := waitEmpty(ctx, tree, force); err != nil {
				return err
			}
		}
	}
	return tree.remove()
}

func waitEmpty(ctx context.Context, tree delegation, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(pollPeriod)
	defer ticker.Stop()
	for {
		populated, err := tree.populated()
		if err != nil || !populated {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
