package tool

import (
	"context"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// idleCounter tracks in-flight HTTP request count. Network events
// arrive on the chromedp listener goroutine; the network-idle wait
// loop reads from a polling goroutine. Mutex-guarded.
type idleCounter struct {
	mu    sync.Mutex
	count int
}

func (c *idleCounter) Inc() {
	c.mu.Lock()
	c.count++
	c.mu.Unlock()
}

func (c *idleCounter) Dec() {
	c.mu.Lock()
	if c.count > 0 {
		c.count--
	}
	c.mu.Unlock()
}

func (c *idleCounter) Snapshot() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// waitNetworkIdle blocks until the in-flight network-request count
// has stayed at or below `threshold` continuously for `period`, or
// until `deadline` elapses. Subscribes to chromedp's network events
// for the duration.
//
// threshold = 2 + period = 500ms is the Puppeteer "networkidle2"
// default — tolerant of analytics beacons and long-poll WebSockets
// that never close. threshold = 0 is the strict "networkidle0" mode.
func waitNetworkIdle(ctx context.Context, threshold int, period, deadline time.Duration) error {
	counter := &idleCounter{}
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch ev.(type) {
		case *network.EventRequestWillBeSent:
			counter.Inc()
		case *network.EventLoadingFinished, *network.EventLoadingFailed:
			counter.Dec()
		}
	})

	deadlineAt := time.Now().Add(deadline)
	var idleSince time.Time
	pollInterval := 50 * time.Millisecond
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			now := time.Now()
			if counter.Snapshot() <= threshold {
				if idleSince.IsZero() {
					idleSince = now
				} else if now.Sub(idleSince) >= period {
					return nil
				}
			} else {
				idleSince = time.Time{}
			}
			if now.After(deadlineAt) {
				// Deadline hit — exit best-effort, the page is what we
				// have. Caller treats this as success and reads outerHTML.
				return nil
			}
		}
	}
}
