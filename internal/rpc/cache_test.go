package rpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func counter(v *atomic.Int32, delay time.Duration) func(context.Context) (any, error) {
	return func(context.Context) (any, error) {
		time.Sleep(delay)
		return int(v.Add(1)), nil
	}
}

func TestCacheSharesConcurrentFetches(t *testing.T) {
	c := newCache(time.Minute, time.Second)
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, _, err := c.get(context.Background(), "k", time.Minute, false, counter(&calls, 20*time.Millisecond)); err != nil || v != 1 {
				t.Errorf("got %v, %v", v, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("%d fetches, want 1", calls.Load())
	}
	// Fresh: answered from memory.
	c.get(context.Background(), "k", time.Minute, false, counter(&calls, 0))
	if calls.Load() != 1 {
		t.Fatalf("fresh entry refetched")
	}
}

func TestCacheBackgroundRefresh(t *testing.T) {
	c := newCache(time.Minute, time.Second)
	var calls atomic.Int32
	c.get(context.Background(), "k", 30*time.Millisecond, false, counter(&calls, 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.run(ctx, 5*time.Millisecond)
	time.Sleep(150 * time.Millisecond)
	if n := calls.Load(); n < 3 {
		t.Fatalf("%d fetches in 150ms at a 30ms interval; the loop is not refreshing", n)
	}
	// Reads never wait: the value is already there.
	start := time.Now()
	c.get(context.Background(), "k", 30*time.Millisecond, false, counter(&calls, time.Second))
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("read waited on a fetch")
	}
}

func TestCacheIdleEntriesStopRefreshing(t *testing.T) {
	c := newCache(20*time.Millisecond, time.Second)
	var calls, pinned atomic.Int32
	c.get(context.Background(), "idle", 5*time.Millisecond, false, counter(&calls, 0))
	c.get(context.Background(), "pinned", 5*time.Millisecond, true, counter(&pinned, 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.run(ctx, 2*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	idle := calls.Load()
	time.Sleep(60 * time.Millisecond)
	if calls.Load() != idle {
		t.Errorf("unread entry still refreshed (%d -> %d)", idle, calls.Load())
	}
	if pinned.Load() < 10 {
		t.Errorf("pinned entry refreshed only %d times", pinned.Load())
	}
	c.mu.Lock()
	_, kept := c.entries["idle"]
	c.mu.Unlock()
	if kept {
		t.Error("long-idle entry not dropped")
	}
}

func TestCacheInvalidateDiscardsInflight(t *testing.T) {
	c := newCache(time.Minute, time.Second)
	state := atomic.Int32{}
	state.Store(1)
	release := make(chan struct{})
	slow := func(context.Context) (any, error) {
		v := state.Load() // reads the state before the change
		<-release
		return int(v), nil
	}
	go c.get(context.Background(), "k", time.Minute, false, slow)
	time.Sleep(10 * time.Millisecond)
	state.Store(2) // an action changes the daemon...
	c.invalidate() // ...and invalidates while the old fetch is in flight
	close(release)
	v, _, _ := c.get(context.Background(), "k", time.Minute, false, func(context.Context) (any, error) { return int(state.Load()), nil })
	if v != 2 {
		t.Fatalf("got %v after invalidate, want the post-change value 2", v)
	}
}

func TestCacheKeepsErrors(t *testing.T) {
	c := newCache(time.Minute, time.Second)
	var calls atomic.Int32
	boom := errors.New("daemon down")
	fail := func(context.Context) (any, error) { calls.Add(1); return nil, boom }
	for i := 0; i < 3; i++ {
		if _, _, err := c.get(context.Background(), "k", time.Minute, false, fail); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("an unreachable daemon was asked %d times; errors should be cached too", calls.Load())
	}
}
