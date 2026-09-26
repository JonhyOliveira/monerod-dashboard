// Package server serves the dashboard UI and its JSON API.
package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
	"github.com/jonhyoliveira/monerod-dashboard/internal/status"
)

//go:embed web
var webFS embed.FS

//go:embed templates/index.html
var indexHTML string

var indexTmpl = template.Must(template.New("index").Parse(indexHTML))

// Daemon is the subset of the RPC client the server needs.
type Daemon interface {
	GetInfo(ctx context.Context) (*rpc.GetInfoResult, error)
	GetLastBlockHeader(ctx context.Context) (*rpc.BlockHeader, error)
}

// Server wires HTTP handlers to a daemon.
type Server struct {
	daemon  Daemon
	refresh time.Duration
	now     func() time.Time
}

// New returns a Server. refresh is the UI poll interval.
func New(d Daemon, refresh time.Duration) *Server {
	return &Server{daemon: d, refresh: refresh, now: time.Now}
}

// StatusResponse is the body of GET /api/status.
type StatusResponse struct {
	OK             bool             `json:"ok"`
	Error          string           `json:"error,omitempty"`
	FetchedAt      time.Time        `json:"fetched_at"`
	RefreshSeconds float64          `json:"refresh_seconds"`
	Node           *status.Node     `json:"node,omitempty"`
	Warnings       []status.Warning `json:"warnings"`
}

// Handler returns the HTTP handler for all routes.
func (s *Server) Handler() http.Handler {
	static, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	return mux
}

// handleIndex renders the dashboard with the current status already filled
// in, so the first paint shows data without waiting on app.js.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	if err := indexTmpl.Execute(&buf, newPage(s.Status(r.Context()))); err != nil {
		log.Printf("rendering index: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes())
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := s.Status(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("writing status: %v", err)
	}
}

// Status queries the daemon and builds the API response. It never fails:
// an unreachable daemon is reported in the response body.
func (s *Server) Status(ctx context.Context) StatusResponse {
	resp := StatusResponse{
		FetchedAt:      s.now().UTC(),
		RefreshSeconds: s.refresh.Seconds(),
		Warnings:       []status.Warning{},
	}

	info, err := s.daemon.GetInfo(ctx)
	if err != nil {
		resp.Error = err.Error()
		resp.Warnings = append(resp.Warnings, status.Warning{
			Level: "error", Code: "unreachable", Message: "Cannot reach monerod: " + err.Error(),
		})
		return resp
	}
	// The last block header only feeds the "last block age" figure, so a
	// failure here degrades the view rather than failing it.
	last, err := s.daemon.GetLastBlockHeader(ctx)
	if err != nil {
		log.Printf("get_last_block_header: %v", err)
		last = nil
	}

	node, ws := status.Build(info, last, s.now())
	resp.OK = true
	resp.Node = &node
	resp.Warnings = append(resp.Warnings, ws...)
	return resp
}
