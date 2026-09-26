package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func jsonRPC(t *testing.T, result any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json_rpc" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req struct {
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		res := result
		if m, ok := result.(map[string]any); ok {
			res = m[req.Method]
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "0", "result": res})
	}
}

func TestGetInfo(t *testing.T) {
	srv := httptest.NewServer(jsonRPC(t, map[string]any{
		"get_info": map[string]any{
			"status": "OK", "height": 3000000, "target_height": 3000010,
			"nettype": "mainnet", "wide_difficulty": "0x5a5b8c0d1e2f",
		},
	}))
	defer srv.Close()

	info, err := New(Options{URL: srv.URL + "/"}).GetInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Height != 3000000 || info.TargetHeight != 3000010 || info.Nettype != "mainnet" || info.WideDifficulty != "0x5a5b8c0d1e2f" {
		t.Fatalf("unexpected result: %+v", info)
	}
}

func TestRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":"0","error":{"code":-32601,"message":"Method not found"}}`))
	}))
	defer srv.Close()

	_, err := New(Options{URL: srv.URL}).GetInfo(context.Background())
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32601 {
		t.Fatalf("want rpc error -32601, got %v", err)
	}
}

func TestBadDaemonStatus(t *testing.T) {
	srv := httptest.NewServer(jsonRPC(t, map[string]any{"get_info": map[string]any{"status": "BUSY"}}))
	defer srv.Close()
	if _, err := New(Options{URL: srv.URL}).GetInfo(context.Background()); err == nil || !strings.Contains(err.Error(), "BUSY") {
		t.Fatalf("want BUSY error, got %v", err)
	}
}

func TestTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)

	start := time.Now()
	_, err := New(Options{URL: srv.URL, Timeout: 50 * time.Millisecond}).GetInfo(context.Background())
	if err == nil {
		t.Fatal("want timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("timeout not enforced: %v", time.Since(start))
	}
}

// digestServer emulates monerod --rpc-login: it challenges unauthenticated
// requests and verifies MD5 qop=auth responses.
func digestServer(t *testing.T, user, pass string, next http.Handler) (*httptest.Server, *atomic.Int32) {
	const realm, nonce = "monero-rpc", "abc123nonce"
	var challenges atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			challenges.Add(1)
			w.Header().Add("WWW-Authenticate", `Digest qop="auth",algorithm=MD5,realm="`+realm+`",nonce="`+nonce+`",stale=false`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		p := parseParams(strings.TrimPrefix(auth, "Digest "))
		ha1 := md5hex(user + ":" + realm + ":" + pass)
		ha2 := md5hex(r.Method + ":" + p["uri"])
		want := md5hex(strings.Join([]string{ha1, nonce, p["nc"], p["cnonce"], p["qop"], ha2}, ":"))
		if p["username"] != user || p["nonce"] != nonce || p["response"] != want || p["uri"] != r.URL.RequestURI() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})), &challenges
}

func TestDigestAuth(t *testing.T) {
	srv, challenges := digestServer(t, "alice", "s3cret", jsonRPC(t, map[string]any{
		"get_info": map[string]any{"status": "OK", "height": 42},
	}))
	defer srv.Close()

	c := New(Options{URL: srv.URL, User: "alice", Pass: "s3cret"})
	for i := 0; i < 3; i++ {
		info, err := c.GetInfo(context.Background())
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if info.Height != 42 {
			t.Fatalf("height = %d", info.Height)
		}
	}
	if n := challenges.Load(); n != 1 {
		t.Errorf("challenge should be cached; got %d challenges", n)
	}
}

func TestDigestAuthWrongPassword(t *testing.T) {
	srv, _ := digestServer(t, "alice", "s3cret", jsonRPC(t, nil))
	defer srv.Close()

	_, err := New(Options{URL: srv.URL, User: "alice", Pass: "nope"}).GetInfo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want 401 error, got %v", err)
	}
}

func TestParseChallenge(t *testing.T) {
	// monerod may send MD5-sess and MD5 challenges comma-joined.
	c := parseChallenge(`Digest qop="auth",algorithm=MD5-sess,realm="monero-rpc",nonce="n1",stale=false, Digest qop="auth",algorithm=MD5,realm="monero-rpc",nonce="n2",stale=false`)
	if c == nil || c.nonce != "n1" || c.algorithm != "MD5-sess" || c.realm != "monero-rpc" {
		t.Fatalf("unexpected challenge %+v", c)
	}
	if parseChallenge(`Basic realm="x"`) != nil {
		t.Fatal("basic challenge should be ignored")
	}
	if parseChallenge(`Digest realm="x",nonce="n",algorithm=SHA-256`) != nil {
		t.Fatal("unsupported algorithm should be ignored")
	}
}
