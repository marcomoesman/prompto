package tool

import (
	"sync"
	"testing"
)

func TestIdleCounter_IncDec(t *testing.T) {
	c := &idleCounter{}
	if got := c.Snapshot(); got != 0 {
		t.Errorf("initial = %d, want 0", got)
	}
	c.Inc()
	c.Inc()
	c.Inc()
	if got := c.Snapshot(); got != 3 {
		t.Errorf("after 3 Inc = %d, want 3", got)
	}
	c.Dec()
	c.Dec()
	if got := c.Snapshot(); got != 1 {
		t.Errorf("after 2 Dec = %d, want 1", got)
	}
}

func TestIdleCounter_DecBelowZeroIsClamped(t *testing.T) {
	c := &idleCounter{}
	c.Dec() // should not panic, should clamp to 0
	c.Dec()
	if got := c.Snapshot(); got != 0 {
		t.Errorf("clamped count = %d, want 0", got)
	}
}

func TestIdleCounter_Concurrent(t *testing.T) {
	c := &idleCounter{}
	const N = 1000
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Go(func() {
			c.Inc()
		})
	}
	wg.Wait()
	if got := c.Snapshot(); got != N {
		t.Errorf("after %d concurrent Inc = %d, want %d", N, got, N)
	}
}
