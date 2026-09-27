// Package rpc is a client for monerod's JSON RPC interface: the JSON-RPC
// methods under /json_rpc and the "other" JSON endpoints such as
// /get_transactions. The binary (.bin) endpoints are not supported; each has
// a JSON equivalent.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxResponse caps how much of a response is read.
const maxResponse = 1 << 30

// Client talks to a single monerod instance.
type Client struct {
	base string
	http *http.Client
}

// Options configures a Client.
type Options struct {
	// URL is the daemon's base RPC URL, e.g. http://127.0.0.1:18081.
	URL string
	// User and Pass enable HTTP digest auth (monerod --rpc-login).
	User, Pass string
	// Timeout bounds each HTTP request. Zero means 30s.
	Timeout time.Duration
	// Transport overrides the underlying transport (mainly for tests).
	Transport http.RoundTripper
}

// New returns a Client for the given options.
func New(o Options) *Client {
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second // some management calls (prune, pop_blocks) are slow
	}
	var rt http.RoundTripper = o.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	if o.User != "" || o.Pass != "" {
		rt = newDigestTransport(o.User, o.Pass, rt)
	}
	return &Client{
		base: strings.TrimRight(o.URL, "/"),
		http: &http.Client{Timeout: o.Timeout, Transport: rt},
	}
}

// Error is a JSON-RPC level error returned by the daemon.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// ErrUnsupported means the daemon does not offer the method: it is too old,
// or the method is disabled on a restricted RPC port.
var ErrUnsupported = errors.New("not supported by this daemon (older version or restricted RPC)")

// StatusError is a response whose "status" field is not "OK".
type StatusError struct {
	Method, Status string
}

func (e *StatusError) Error() string { return fmt.Sprintf("%s: %s", e.Method, e.Status) }

// Status is embedded in every result that carries monerod's status field.
type Status struct {
	Status    string `json:"status"`
	Untrusted bool   `json:"untrusted"`
}

func (s Status) status() string { return s.Status }

type statusCarrier interface{ status() string }

// checkStatus turns a non-OK status into an error. An empty status is
// accepted, since some responses omit it.
func checkStatus(method string, out any) error {
	if sc, ok := out.(statusCarrier); ok {
		if st := sc.status(); st != "" && st != "OK" {
			return &StatusError{Method: method, Status: st}
		}
	}
	return nil
}

// Call invokes a JSON-RPC method at /json_rpc and decodes its result into
// out. params may be nil.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	if params == nil {
		params = struct{}{}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "0", "method": method, "params": params})
	if err != nil {
		return err
	}
	raw, err := c.post(ctx, "/json_rpc", method, body)
	if err != nil {
		return err
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%s: decoding response: %w", method, err)
	}
	if env.Error != nil {
		if env.Error.Code == -32601 { // method not found
			return fmt.Errorf("%s: %w", method, ErrUnsupported)
		}
		return fmt.Errorf("%s: %w", method, env.Error)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("%s: decoding result: %w", method, err)
	}
	return checkStatus(method, out)
}

// CallPath invokes one of the JSON endpoints outside /json_rpc, such as
// "/get_transactions". req may be nil.
func (c *Client) CallPath(ctx context.Context, path string, req, out any) error {
	if req == nil {
		req = struct{}{}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	name := strings.TrimPrefix(path, "/")
	raw, err := c.post(ctx, path, name, body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: decoding response: %w", name, err)
	}
	return checkStatus(name, out)
}

// Raw sends a request and returns the undecoded response body. It backs the
// dashboard's RPC console. For path "/json_rpc", method and params form a
// JSON-RPC request; otherwise params is posted as the body.
func (c *Client) Raw(ctx context.Context, path, method string, params json.RawMessage) ([]byte, error) {
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	body := []byte(params)
	name := strings.TrimPrefix(path, "/")
	if path == "/json_rpc" {
		var err error
		body, err = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "0", "method": method, "params": params})
		if err != nil {
			return nil, err
		}
		name = method
	}
	return c.post(ctx, path, name, body)
}

func (c *Client) post(ctx context.Context, path, name string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	defer resp.Body.Close()
	// Generous: a busy mainnet mempool (get_transaction_pool includes every
	// transaction's decoded JSON) runs to hundreds of MiB.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if len(raw) > maxResponse {
		return nil, fmt.Errorf("%s: response larger than %d MiB", name, maxResponse>>20)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: reading response: %w", name, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%s: %w", name, ErrUnsupported)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%s: unexpected HTTP status %s", name, resp.Status)
	}
	return raw, nil
}
