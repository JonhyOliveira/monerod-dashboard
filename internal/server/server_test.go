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
	for _, p := range []string{"/", "/app.js", "/style.css", "/healthz"} {
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
