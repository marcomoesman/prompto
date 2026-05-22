package agent

import (
	"context"
	"fmt"
)

// ProviderGate caps concurrent Provider.Complete calls plus concurrent task
// subagent spawns for a given (provider, model) pair. One gate is constructed
// per Agent at startup and shared by every child Run that the agent spawns,
// so a MaxParallelJobs=1 setup naturally serializes everything that touches
// the same backend — primary streaming, summarizer calls, parallel children.
//
// The gate is goroutine-safe and cheap; Acquire blocks via a buffered channel
// with capacity == MaxParallelJobs. Release is matched 1:1 with Acquire.
type ProviderGate struct {
	sem chan struct{}
}

// NewProviderGate returns a gate with the given concurrency cap. A cap <= 0
// is treated as a single permit (the safest default for unknown backends);
// callers that want "effectively unbounded" should pass UnboundedParallel
// from the config package.
func NewProviderGate(maxParallel int) *ProviderGate {
	if maxParallel <= 0 {
		maxParallel = 1
	}
	return &ProviderGate{sem: make(chan struct{}, maxParallel)}
}

// Acquire blocks until a permit is available or ctx is cancelled. Each
// successful Acquire must be paired with exactly one Release.
func (g *ProviderGate) Acquire(ctx context.Context) error {
	if g == nil {
		return nil
	}
	select {
	case g.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("provider gate acquire: %w", ctx.Err())
	}
}

// Release returns a previously acquired permit. Calling Release without a
// prior successful Acquire panics — that always indicates a leaked permit.
func (g *ProviderGate) Release() {
	if g == nil {
		return
	}
	select {
	case <-g.sem:
	default:
		panic("provider gate: Release without matching Acquire")
	}
}

// Capacity returns the configured maximum number of concurrent permits.
// Useful for tests and for diagnostic logging.
func (g *ProviderGate) Capacity() int {
	if g == nil {
		return 0
	}
	return cap(g.sem)
}
