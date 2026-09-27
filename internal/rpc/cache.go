package rpc

import (
	"context"
	"sync"
	"time"
)

// cache holds the latest result (value or error) of each read call, keyed
// by method and parameters. A background loop (run) refreshes entries before
// they go stale, so readers are answered from memory instead of waiting on
// the daemon.
//
// Entries stay warm while they are being read: one not read for idleAfter
// stops being refreshed and is eventually dropped. Pinned entries are
// refreshed regardless, so the pages everyone opens first are always ready.
type cache struct {
	now       func() time.Time
	idleAfter time.Duration
	timeout   time.Duration // per background fetch

	mu      sync.Mutex
	entries map[string]*entry
	wake    chan struct{}
}

type entry struct {
	every  time.Duration // refresh interval
	pinned bool
	fetch  func(context.Context) (any, error)

	val   any
	err   error
	at    time.Time // when val/err were fetched
	valid bool
	used  time.Time // last read

	gen      uint64        // bumped by invalidate; results of older fetches are discarded
	inflight chan struct{} // closed when the current fetch finishes
	infGen   uint64        // generation the in-flight fetch belongs to
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

// get returns the cached result for key, fetching it synchronously only if
// there is none yet or it is far out of date (e.g. nobody read it for a
// while, so the background loop stopped refreshing it). Concurrent callers
// share one fetch.
func (c *cache) get(ctx context.Context, key string, every time.Duration, pinned bool, fetch func(context.Context) (any, error)) (any, time.Time, error) {
	c.mu.Lock()
	e := c.entries[key]
	if e == nil {
		e = &entry{every: every, pinned: pinned, fetch: fetch}
		c.entries[key] = e
	}
	e.fetch = fetch // latest closure; parameters are part of the key
	e.used = c.now()
	for {
		// Serve anything the background loop could have refreshed; older
		// than that means the entry fell out of rotation.
		if e.valid && c.now().Sub(e.at) < 3*e.every {
			v, at, err := e.val, e.at, e.err
			c.mu.Unlock()
			return v, at, err
		}
		ch := c.start(key, e)
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, time.Time{}, ctx.Err()
		}
		c.mu.Lock()
		if e.valid {
			v, at, err := e.val, e.at, e.err
			c.mu.Unlock()
			return v, at, err
		}
		// Invalidated while we waited: fetch again.
	}
}

// start begins a fetch of e unless one for the current generation is
// already running, and returns the channel closed when it finishes.
// Callers hold c.mu.
func (c *cache) start(key string, e *entry) chan struct{} {
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
			e.val, e.err, e.at, e.valid = v, err, c.now(), true
		}
		if e.inflight == ch {
			e.inflight = nil
		}
		c.mu.Unlock()
		close(ch)
	}()
	return ch
}

// invalidate forgets every cached result, so the next read fetches fresh
// data. Used after anything that changes the daemon's state.
func (c *cache) invalidate() {
	c.mu.Lock()
	for _, e := range c.entries {
		e.gen++
		e.valid = false
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
			// Not read lately: let it age; the next read refetches.
		case !e.valid || now.Sub(e.at) >= e.every:
			c.start(key, e)
		}
	}
}
