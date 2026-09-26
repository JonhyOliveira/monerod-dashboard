// Package rpc is a minimal client for monerod's JSON-RPC interface.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to a single monerod instance.
type Client struct {
	endpoint string
	http     *http.Client
}

// Options configures a Client.
type Options struct {
	// URL is the daemon's base RPC URL, e.g. http://127.0.0.1:18081.
	URL string
	// User and Pass enable HTTP digest auth (monerod --rpc-login).
	User, Pass string
	// Timeout bounds each HTTP request. Zero means 5s.
	Timeout time.Duration
	// Transport overrides the underlying transport (mainly for tests).
	Transport http.RoundTripper
}

// New returns a Client for the given options.
func New(o Options) *Client {
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	var rt http.RoundTripper = o.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	if o.User != "" || o.Pass != "" {
		rt = newDigestTransport(o.User, o.Pass, rt)
	}
	return &Client{
		endpoint: strings.TrimRight(o.URL, "/") + "/json_rpc",
		http:     &http.Client{Timeout: o.Timeout, Transport: rt},
	}
}

// Error is a JSON-RPC level error returned by the daemon.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// Call invokes a JSON-RPC method and decodes its result into out.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("%s: unexpected HTTP status %s", method, resp.Status)
	}

	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&env); err != nil {
		return fmt.Errorf("%s: decoding response: %w", method, err)
	}
	if env.Error != nil {
		return fmt.Errorf("%s: %w", method, env.Error)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("%s: decoding result: %w", method, err)
	}
	return nil
}

// GetInfo calls get_info.
func (c *Client) GetInfo(ctx context.Context) (*GetInfoResult, error) {
	var r GetInfoResult
	if err := c.Call(ctx, "get_info", nil, &r); err != nil {
		return nil, err
	}
	if r.Status != "" && r.Status != "OK" {
		return nil, fmt.Errorf("get_info: daemon status %q", r.Status)
	}
	return &r, nil
}

// GetLastBlockHeader calls get_last_block_header.
func (c *Client) GetLastBlockHeader(ctx context.Context) (*BlockHeader, error) {
	var r getLastBlockHeaderResult
	if err := c.Call(ctx, "get_last_block_header", nil, &r); err != nil {
		return nil, err
	}
	if r.Status != "" && r.Status != "OK" {
		return nil, fmt.Errorf("get_last_block_header: daemon status %q", r.Status)
	}
	return &r.BlockHeader, nil
}
