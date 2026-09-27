package rpc

import (
	"context"
	"sync"
	"time"
)

// Freshness collects how up to date the cached data behind one request is.
// Attach one to a request's context with WithFreshness; every cached read
// made with that context reports into it.
type Freshness struct {
	mu     sync.Mutex
	oldest time.Time // fetch time of the oldest value used
	stale  bool      // some value is behind schedule or its refresh failed
	err    error     // the latest refresh error, if one failed
	now    func() time.Time
}

type freshnessKey struct{}

// WithFreshness returns a context whose cached reads report into f.
func WithFreshness(ctx context.Context) (context.Context, *Freshness) {
	f := &Freshness{now: time.Now}
	return context.WithValue(ctx, freshnessKey{}, f), f
}

// FreshnessFrom returns the Freshness attached to ctx, or nil.
func FreshnessFrom(ctx context.Context) *Freshness { return freshnessFrom(ctx) }

func freshnessFrom(ctx context.Context) *Freshness {
	f, _ := ctx.Value(freshnessKey{}).(*Freshness)
	return f
}

// note records one cached read.
func (f *Freshness) note(r cachedResult) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Nothing shown from this entry: its section shows the error itself;
	// there is no old data to flag.
	if !r.hasVal {
		return
	}
	if f.oldest.IsZero() || r.okAt.Before(f.oldest) {
		f.oldest = r.okAt
	}
	// Stale means the data shown is old: behind by more than two refresh
	// intervals. A single failed refresh of recent data is not flagged; the
	// latest error only explains why data went stale.
	if f.now().Sub(r.okAt) > 2*r.every {
		f.stale = true
		if r.err != nil {
			f.err = r.err
		}
	}
}

// Oldest is when the oldest value used was fetched (zero if none).
func (f *Freshness) Oldest() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.oldest
}

// Stale reports whether any value used is out of date, and the latest
// refresh error if one failed (nil if refreshes are only running late).
func (f *Freshness) Stale() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stale, f.err
}
