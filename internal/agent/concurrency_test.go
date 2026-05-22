package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderGate_BlocksWhenSaturated(t *testing.T) {
	g := NewProviderGate(2)

	if err := g.Acquire(t.Context()); err != nil {
		t.Fatalf("Acquire 1: %v", err)
	}
	if err := g.Acquire(t.Context()); err != nil {
		t.Fatalf("Acquire 2: %v", err)
	}

	// Third acquire should block until a Release fires.
	var acquired atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := g.Acquire(ctx); err != nil {
			t.Errorf("blocked Acquire: %v", err)
			return
		}
		acquired.Store(true)
		g.Release()
	}()

	// Give the goroutine a chance to actually block.
	time.Sleep(20 * time.Millisecond)
	if acquired.Load() {
		t.Fatal("third Acquire should have blocked")
	}

	g.Release()
	<-done
	if !acquired.Load() {
		t.Fatal("third Acquire never unblocked after Release")
	}
	g.Release()
}

func TestProviderGate_ContextCancelReleases(t *testing.T) {
	g := NewProviderGate(1)
	if err := g.Acquire(t.Context()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- g.Acquire(ctx) }()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled Acquire did not return")
	}

	// The cancelled call must not have consumed a permit; the next Acquire
	// should still block on the original holder.
	immediate, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if err := g.Acquire(immediate); err == nil {
		t.Fatal("expected timeout — gate should still be saturated")
		g.Release()
	}

	g.Release() // release the original holder
}

func TestProviderGate_NilSafe(t *testing.T) {
	var g *ProviderGate
	if err := g.Acquire(t.Context()); err != nil {
		t.Fatalf("nil gate Acquire: %v", err)
	}
	g.Release()
	if g.Capacity() != 0 {
		t.Errorf("nil gate Capacity = %d, want 0", g.Capacity())
	}
}

func TestProviderGate_CapacityZeroDefaults(t *testing.T) {
	g := NewProviderGate(0)
	if g.Capacity() != 1 {
		t.Errorf("Capacity = %d, want 1 (defaulted)", g.Capacity())
	}
}

func TestProviderGate_ReleaseWithoutAcquirePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unmatched Release")
		}
	}()
	g := NewProviderGate(1)
	g.Release()
}
