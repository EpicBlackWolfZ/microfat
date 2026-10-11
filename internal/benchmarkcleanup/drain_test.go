package benchmarkcleanup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeDelegation struct {
	live, termDrains, forceDrains         bool
	readErr, termErr, forceErr, removeErr error
	terms, forces, removals               int
}

func (f *fakeDelegation) populated() (bool, error) { return f.live, f.readErr }
func (f *fakeDelegation) terminate() error {
	f.terms++
	if f.termDrains {
		f.live = false
	}
	return f.termErr
}
func (f *fakeDelegation) force() error {
	f.forces++
	if f.forceDrains {
		f.live = false
	}
	return f.forceErr
}
func (f *fakeDelegation) remove() error { f.removals++; return f.removeErr }

func TestBoundedDrain(t *testing.T) {
	t.Parallel()
	failure := errors.New("injected failure")
	for _, test := range []struct {
		name                    string
		tree                    fakeDelegation
		err                     error
		terms, forces, removals int
	}{
		{"empty", fakeDelegation{}, nil, 0, 0, 1},
		{"graceful", fakeDelegation{live: true, termDrains: true}, nil, 1, 0, 1},
		{"force", fakeDelegation{live: true, forceDrains: true}, nil, 1, 1, 1},
		{"read failure", fakeDelegation{readErr: failure}, failure, 0, 0, 0},
		{"term failure", fakeDelegation{live: true, termErr: failure}, failure, 1, 0, 0},
		{"force failure", fakeDelegation{live: true, forceErr: failure}, failure, 1, 1, 0},
		{"forced drain timeout", fakeDelegation{live: true}, context.DeadlineExceeded, 1, 1, 0},
		{"remove failure", fakeDelegation{removeErr: failure}, failure, 0, 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := drain(t.Context(), &test.tree, time.Millisecond, time.Millisecond)
			require.ErrorIs(t, err, test.err)
			require.Equal(t, test.terms, test.tree.terms)
			require.Equal(t, test.forces, test.tree.forces)
			require.Equal(t, test.removals, test.tree.removals)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	tree := &fakeDelegation{live: true}
	require.ErrorIs(t, drain(ctx, tree, time.Second, time.Second), context.Canceled)
	require.Zero(t, tree.forces)
	require.Zero(t, tree.removals)
	tree.readErr = failure
	require.ErrorIs(t, waitEmpty(t.Context(), tree, time.Second), failure)
}
