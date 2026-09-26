package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
	"github.com/jonhyoliveira/monerod-dashboard/internal/status"
)

type fakeDaemon struct {
	info    *rpc.GetInfoResult
	infoErr error
	last    *rpc.BlockHeader
	lastErr error
}

func (f *fakeDaemon) GetInfo(context.Context) (*rpc.GetInfoResult, error) { return f.info, f.infoErr }
func (f *fakeDaemon) GetLastBlockHeader(context.Context) (*rpc.BlockHeader, error) {
	return f.last, f.lastErr
}

func getStatus(t *testing.T, d Daemon) StatusResponse {
	t.Helper()
	srv := httptest.NewServer(New(d, 7*time.Second).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("status %d, content-type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var s StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStatusOK(t *testing.T) {
	s := getStatus(t, &fakeDaemon{
		info: &rpc.GetInfoResult{Status: "OK", Height: 100, Synchronized: true, OutgoingConnectionsCount: 8, Nettype: "stagenet"},
		last: &rpc.BlockHeader{Timestamp: time.Now().Unix() - 60},
	})
	if !s.OK || s.Node == nil || s.Node.Height != 100 || s.Node.Nettype != "stagenet" || s.RefreshSeconds != 7 {
		t.Fatalf("unexpected response %+v", s)
	}
	if s.Node.LastBlockTime == 0 {
		t.Error("last block time missing")
	}
}

func TestStatusLastHeaderFailureDegrades(t *testing.T) {
	s := getStatus(t, &fakeDaemon{
		info:    &rpc.GetInfoResult{Status: "OK", Height: 100, Synchronized: true, OutgoingConnectionsCount: 8},
		lastErr: errors.New("restricted"),
	})
	if !s.OK || s.Node.LastBlockTime != 0 {
		t.Fatalf("unexpected response %+v", s)
	}
}

func TestStatusUnreachable(t *testing.T) {
	s := getStatus(t, &fakeDaemon{infoErr: errors.New("connection refused")})
	if s.OK || s.Node != nil || !strings.Contains(s.Error, "connection refused") {
		t.Fatalf("unexpected response %+v", s)
	}
	if len(s.Warnings) != 1 || s.Warnings[0].Code != "unreachable" {
		t.Fatalf("warnings = %+v", s.Warnings)
	}
}

func TestStaticAssets(t *testing.T) {
	srv := httptest.NewServer(New(&fakeDaemon{}, time.Second).Handler())
	defer srv.Close()
	for _, p := range []string{"/app.js", "/style.css", "/healthz"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || len(body) == 0 {
			t.Errorf("%s: status %d, %d bytes", p, resp.StatusCode, len(body))
		}
	}
}

func TestTemplateNotServedRaw(t *testing.T) {
	srv := httptest.NewServer(New(&fakeDaemon{infoErr: errors.New("down")}, time.Second).Handler())
	defer srv.Close()
	for _, p := range []string{"/index.html", "/templates/index.html"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		// FileServer redirects /index.html to /, which is rendered; the raw
		// template source must never be served.
		if strings.Contains(string(body), "{{") {
			t.Errorf("%s: raw template served", p)
		}
	}
}

var nodeFixture = status.Node{
	State: "syncing", StateLabel: "Syncing (55 blocks behind)",
	Height: 3212345, TargetHeight: 3212400, BlocksBehind: 55, SyncPercent: 99.99828788444776,
	Nettype: "mainnet", Version: "0.18.4.2-release",
}

func getIndex(t *testing.T, d Daemon) string {
	t.Helper()
	srv := httptest.NewServer(New(d, 5*time.Second).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("status %d, content-type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return string(body)
}

// initialJSON extracts and decodes the status embedded for app.js.
func initialJSON(t *testing.T, html string) StatusResponse {
	t.Helper()
	const open = `<script id="initial" type="application/json">`
	i := strings.Index(html, open)
	if i < 0 {
		t.Fatal("initial status script missing")
	}
	rest := html[i+len(open):]
	j := strings.Index(rest, "</script>")
	var s StatusResponse
	if err := json.Unmarshal([]byte(rest[:j]), &s); err != nil {
		t.Fatalf("initial status is not valid JSON: %v\n%s", err, rest[:j])
	}
	return s
}

func TestIndexServerRendered(t *testing.T) {
	now := time.Now().Unix()
	html := getIndex(t, &fakeDaemon{
		info: &rpc.GetInfoResult{
			Status: "OK", Height: 3212345, TargetHeight: 3212400, Nettype: "mainnet",
			Version: "0.18.4.2-release", UpdateAvailable: true, OutgoingConnectionsCount: 12,
			IncomingConnectionsCount: 37, Difficulty: 412287134261, Target: 120,
			TxPoolSize: 43, DatabaseSize: 243 << 30, FreeSpace: 500 << 30, StartTime: now - 277200,
		},
		last: &rpc.BlockHeader{Timestamp: now - 95},
	})
	for _, want := range []string{
		`<span id="state-label">Syncing (55 blocks behind)</span>`,
		`<dd id="height">3,212,345</dd>`,
		`<dd id="sync-percent">99.99%</dd>`,
		`<dd id="peers-out" class="big">12</dd>`,
		`<dd id="hashrate">3.44 GH/s</dd>`,
		`<dd id="difficulty">412,287,134,261</dd>`,
		`<dd id="last-block">1m 35s ago</dd>`,
		`<dd id="txpool" class="big">43</dd>`,
		`<dd id="db-size">243.0 GiB</dd>`,
		`<dd id="uptime" class="big">3d 5h</dd>`,
		`<span id="nettype" class="badge">mainnet</span>`,
		`<span id="version">v0.18.4.2-release</span>`,
		`A monerod update is available.`,
		`style="--refresh-duration: 5s"`,
		`<section class="grid">`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s", want)
		}
	}
	if s := initialJSON(t, html); !s.OK || s.Node.Height != 3212345 || s.RefreshSeconds != 5 {
		t.Errorf("initial status = %+v", s)
	}
}

func TestIndexUnreachable(t *testing.T) {
	html := getIndex(t, &fakeDaemon{infoErr: errors.New("connection refused")})
	for _, want := range []string{
		`<span id="state-label">Unreachable</span>`,
		`<section class="grid stale">`,
		`<li class="error">Cannot reach monerod: connection refused</li>`,
		`<dd id="height">—</dd>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s", want)
		}
	}
	if s := initialJSON(t, html); s.OK || s.Error != "connection refused" {
		t.Errorf("initial status = %+v", s)
	}
}

func TestIndexEscapesDaemonData(t *testing.T) {
	evil := `</script><script>alert(1)</script>`
	html := getIndex(t, &fakeDaemon{info: &rpc.GetInfoResult{Status: "OK", Nettype: evil, TopBlockHash: evil}})
	if strings.Contains(html, "<script>alert(1)") {
		t.Fatal("daemon data broke out of its context")
	}
	if s := initialJSON(t, html); s.Node.Nettype != evil {
		t.Errorf("round-tripped nettype = %q", s.Node.Nettype)
	}
}
