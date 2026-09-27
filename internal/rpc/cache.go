package rpc

import (
	"context"
	"sync"
	"time"
)

// cache holds the latest successful result of each read call, keyed by
// method and parameters. A background loop (run) refreshes entries before
// they go stale, so readers are answered from memory instead of waiting on
// the daemon.
//
// A failed refresh does not throw the last good value away: readers keep
// getting it, together with the error and the time it was fetched, so the
// dashboard can say how out of date it is.
//
// Entries stay warm while they are being read: one not read for idleAfter
// stops being refreshed and is eventually dropped. Pinned entries are
// refreshed regardless, so the pages everyone opens first are always ready.
type cache struct {
	now       func() time.Time
	idleAfter time.Duration
	timeout   time.Duration // per fetch

	mu      sync.Mutex
	entries map[string]*entry
	wake    chan struct{}
}

type entry struct {
	every  time.Duration // refresh interval
	pinned bool
	fetch  func(context.Context) (any, error)

	val     any       // last successful result
	hasVal  bool      //
	okAt    time.Time // when val was fetched
	lastErr error     // error of the latest attempt, nil if it succeeded
	tried   time.Time // when the latest attempt finished
	fresh   bool      // false until the first attempt, and after invalidate
	used    time.Time // last read

	gen      uint64        // bumped by invalidate; results of older fetches are discarded
	inflight chan struct{} // closed when the current fetch finishes
	infGen   uint64        // generation the in-flight fetch belongs to
}

// cached is what a read returns.
type cachedResult struct {
	val    any
	hasVal bool
	okAt   time.Time     // when val was fetched
	err    error         // latest attempt's error (val, if any, is older)
	every  time.Duration // the entry's refresh interval
}

func newCache(idleAfter, timeout time.Duration) *cache {
	return &cache{
		now:       time.Now,
		idleAfter: idleAfter,
		timeout:   timeout,
		entries:   map[string]*entry{},
		wake:      make(chan struct{}, 1),
	}
}

// get returns the cached result for key. It only waits for the daemon when
// there is nothing to show yet, or the entry was invalidated (after an
// action, whose effect must be visible). Otherwise it answers immediately,
// and if the entry fell behind (nobody read it for a while) it starts a
// refresh in the background; the caller sees the age in okAt. Concurrent
// callers share one fetch.
func (c *cache) get(ctx context.Context, key string, every time.Duration, pinned bool, fetch func(context.Context) (any, error)) cachedResult {
	c.mu.Lock()
	e := c.entries[key]
	if e == nil {
		e = &entry{every: every, pinned: pinned, fetch: fetch}
		c.entries[key] = e
	}
	e.fetch = fetch // latest closure; parameters are part of the key
	e.used = c.now()
	for {
		if e.fresh {
			if c.now().Sub(e.tried) >= 2*e.every {
				c.start(e) // behind: refresh, but don't make this reader wait
			}
			r := e.result()
			c.mu.Unlock()
			return r
		}
		ch := c.start(e)
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			c.mu.Lock()
			r := e.result()
			c.mu.Unlock()
			if !r.hasVal {
				r.err = ctx.Err()
			}
			return r
		}
		c.mu.Lock()
		// Not fresh after the fetch means it was invalidated meanwhile:
		// fetch again.
	}
}

func (e *entry) result() cachedResult {
	return cachedResult{val: e.val, hasVal: e.hasVal, okAt: e.okAt, err: e.lastErr, every: e.every}
}

// start begins a fetch of e unless one for the current generation is
// already running, and returns the channel closed when it finishes.
// Callers hold c.mu.
func (c *cache) start(e *entry) chan struct{} {
	if e.inflight != nil && e.infGen == e.gen {
		return e.inflight
	}
	ch, gen, fetch := make(chan struct{}), e.gen, e.fetch
	e.inflight, e.infGen = ch, gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		v, err := fetch(ctx)
		cancel()
		c.mu.Lock()
		if e.gen == gen {
			now := c.now()
			e.tried, e.fresh, e.lastErr = now, true, err
			if err == nil {
				e.val, e.hasVal, e.okAt = v, true, now
			}
		}
		if e.inflight == ch {
			e.inflight = nil
		}
		c.mu.Unlock()
		close(ch)
	}()
	return ch
}

// invalidate makes the next read of every entry fetch fresh data. Used after
// anything that changes the daemon's state. Last good values are kept, in
// case the refetch fails.
func (c *cache) invalidate() {
	c.mu.Lock()
	for _, e := range c.entries {
		e.gen++
		e.fresh = false
	}
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// run keeps entries fresh until ctx is done.
func (c *cache) run(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		c.refreshDue()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.wake:
		}
	}
}

func (c *cache) refreshDue() {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, e := range c.entries {
		idle := now.Sub(e.used) > c.idleAfter
		switch {
		case idle && !e.pinned && now.Sub(e.used) > 4*c.idleAfter:
			if e.inflight == nil {
				delete(c.entries, key)
			}
		case idle && !e.pinned:
			// Not read lately: let it age; the next read refreshes it.
		case !e.fresh || now.Sub(e.tried) >= e.every:
			c.start(e)
		}
	}
}
