// Package rpctest provides a fake monerod for tests. It answers with the
// responses recorded from a real regtest daemon in internal/rpc/testdata,
// and records every call so tests can assert what the dashboard sent.
package rpctest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Call is a request the fake received.
type Call struct {
	Method string          // JSON-RPC method, or the path without "/" for other endpoints
	Params json.RawMessage // JSON-RPC params, or the request body
}

// Daemon is a fake monerod.
type Daemon struct {
	*httptest.Server

	mu        sync.Mutex
	calls     []Call
	overrides map[string]func(params json.RawMessage) any
}

// New starts a fake daemon; it is closed when the test ends.
func New(t testing.TB) *Daemon {
	d := &Daemon{overrides: map[string]func(json.RawMessage) any{}}
	d.Server = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.Close)
	return d
}

// Handle overrides the response for a method (JSON-RPC name, or path name
// such as "get_transaction_pool"). For JSON-RPC methods fn returns the
// result; returning an *RPCError sends an error instead. For other
// endpoints fn returns the whole body.
func (d *Daemon) Handle(method string, fn func(params json.RawMessage) any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.overrides[method] = fn
}

// RPCError makes a Handle override answer with a JSON-RPC error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Calls returns the calls received so far with the given method.
func (d *Daemon) Calls(method string) []Call {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Call
	for _, c := range d.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (d *Daemon) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")

	if r.URL.Path == "/json_rpc" {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		json.Unmarshal(body, &req)
		d.record(req.Method, req.Params)
		if fn := d.override(req.Method); fn != nil {
			res := fn(req.Params)
			if e, ok := res.(*RPCError); ok {
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "0", "error": e})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "0", "result": res})
			return
		}
		if raw, ok := fixture(req.Method + ".json"); ok {
			w.Write(raw)
			return
		}
		if res, ok := okMethods[req.Method]; ok {
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "0", "result": res})
			return
		}
		w.Write([]byte(`{"jsonrpc":"2.0","id":"0","error":{"code":-32601,"message":"Method not found"}}`))
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/")
	d.record(name, body)
	if fn := d.override(name); fn != nil {
		json.NewEncoder(w).Encode(fn(body))
		return
	}
	if raw, ok := fixture("path_" + name + ".json"); ok {
		w.Write(raw)
		return
	}
	if res, ok := okPaths[name]; ok {
		json.NewEncoder(w).Encode(res)
		return
	}
	http.NotFound(w, r)
}

// Methods without a recorded fixture (they change the daemon) answer OK.
var okMethods = map[string]any{
	"set_bans":       map[string]string{"status": "OK"},
	"flush_txpool":   map[string]string{"status": "OK"},
	"relay_tx":       map[string]string{"status": "OK"},
	"flush_cache":    map[string]string{"status": "OK"},
	"submit_block":   map[string]string{"status": "OK", "block_id": strings.Repeat("ab", 32)},
	"generateblocks": map[string]any{"status": "OK", "height": 141, "blocks": []string{strings.Repeat("cd", 32)}},
	"calc_pow":       strings.Repeat("ef", 32),
}

var okPaths = map[string]any{
	"set_limit":            map[string]any{"status": "OK", "limit_down": 1024, "limit_up": 512},
	"start_mining":         map[string]string{"status": "OK"},
	"stop_mining":          map[string]string{"status": "OK"},
	"set_log_hash_rate":    map[string]string{"status": "OK"},
	"set_log_level":        map[string]string{"status": "OK"},
	"set_log_categories":   map[string]string{"status": "OK", "categories": "*:WARNING"},
	"save_bc":              map[string]string{"status": "OK"},
	"pop_blocks":           map[string]any{"status": "OK", "height": 121},
	"stop_daemon":          map[string]string{"status": "OK"},
	"send_raw_transaction": map[string]any{"status": "OK", "not_relayed": false},
}

func (d *Daemon) record(method string, params json.RawMessage) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, Call{Method: method, Params: append(json.RawMessage(nil), params...)})
}

func (d *Daemon) override(method string) func(json.RawMessage) any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.overrides[method]
}

func fixture(name string) ([]byte, bool) {
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "testdata", name))
	return raw, err == nil
}
