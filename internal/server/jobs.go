package server

import (
	"context"
	"log"
	"sync"
	"time"
)

// job runs one long daemon operation in the background, such as checking or
// pruning the blockchain, which take minutes to hours on mainnet: far longer
// than a page request or the RPC timeout. Pages show its progress; only one
// runs at a time.
type job struct {
	mu       sync.Mutex
	running  bool
	what     string // what is running or last ran, e.g. "Checking pruning"
	started  time.Time
	finished time.Time
	result   string
	err      error
}

// jobState is a snapshot of a job for templates.
type jobState struct {
	Running  bool
	What     string
	Started  time.Time
	Finished time.Time
	Result   string
	Err      error
}

// maxJobTime bounds a background operation.
const maxJobTime = 12 * time.Hour

// start runs fn in the background unless another run is in progress.
func (j *job) start(what string, fn func(context.Context) (string, error)) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running {
		return false
	}
	j.running, j.what, j.started, j.result, j.err = true, what, time.Now(), "", nil
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), maxJobTime)
		defer cancel()
		res, err := fn(ctx)
		j.mu.Lock()
		j.running, j.finished, j.result, j.err = false, time.Now(), res, err
		j.mu.Unlock()
		log.Printf("job %q finished after %s: result=%q err=%v", what, time.Since(j.started).Round(time.Second), res, err)
	}()
	return true
}

func (j *job) state() jobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return jobState{Running: j.running, What: j.what, Started: j.started, Finished: j.finished, Result: j.result, Err: j.err}
}
