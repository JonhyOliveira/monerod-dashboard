// Package server serves the dashboard: server-rendered pages for every area
// of the node, refreshed and driven by htmx, behind a password login.
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
	"github.com/jonhyoliveira/monerod-dashboard/internal/status"
)

// Config configures a Server.
type Config struct {
	// Refresh is the poll interval of live page sections.
	Refresh time.Duration
	// Password protects the whole dashboard. Required unless NoAuth.
	Password string
	// NoAuth disables the login entirely (for trusted, local-only use).
	NoAuth bool
}

// Server serves the dashboard for one daemon.
type Server struct {
	rpc     *rpc.Node
	refresh time.Duration
	auth    *auth
	pages   *pageSet
	now     func() time.Time
}

// New returns a Server. Reads go through node's cache; run node.Run
// alongside so it stays fresh.
func New(node *rpc.Node, cfg Config) *Server {
	s := &Server{rpc: node, refresh: cfg.Refresh, now: time.Now}
	if s.refresh <= 0 {
		s.refresh = 5 * time.Second
	}
	s.auth = newAuth(cfg.Password, cfg.NoAuth, func() time.Time { return s.now() })
	s.pages = mustLoadPages()
	return s
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

	// Public: login, health check and static assets.
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	staticFiles := http.FileServerFS(static)
	for _, p := range []string{"/app.js", "/style.css", "/favicon.svg", "/vendor/"} {
		mux.Handle("GET "+p, staticFiles)
	}

	// Everything else requires a session.
	protected := http.NewServeMux()
	protected.HandleFunc("POST /logout", s.handleLogout)
	protected.HandleFunc("GET /api/status", s.handleStatus)
	protected.HandleFunc("GET /search", s.handleSearch)
	s.routePages(protected)
	s.routeActions(protected)
	mux.Handle("/", s.requireSession(protected))

	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// No inline scripts. Inline style attributes are allowed for the
		// progress bars; html/template escapes the values.
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; "+
			"style-src-attr 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; "+
			"base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

type ctxKey struct{}

func sessionFrom(r *http.Request) *session {
	s, _ := r.Context().Value(ctxKey{}).(*session)
	return s
}

// requireSession redirects to /login without a session and enforces CSRF
// protection on every state-changing request.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := s.auth.lookup(r)
		if sess == nil && s.auth.disabled {
			sess = s.auth.newSession()
			setSessionCookie(w, r, sess.id, 0)
		}
		if sess == nil {
			switch {
			case r.Header.Get("HX-Request") == "true":
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
			case strings.HasPrefix(r.URL.Path, "/api/"):
				http.Error(w, `{"error":"login required"}`, http.StatusUnauthorized)
			default:
				http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			}
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			token := r.Header.Get("X-CSRF-Token")
			if token == "" {
				token = r.PostFormValue("csrf")
			}
			if !sameOrigin(r) || token != sess.csrf {
				http.Error(w, "invalid or missing CSRF token; reload the page and try again", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, sess)))
	})
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.auth.lookup(r) != nil || s.auth.disabled {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.pages.renderLogin(w, loginData{Next: safeNext(r.URL.Query().Get("next"))})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	next := safeNext(r.PostFormValue("next"))
	if !sameOrigin(r) {
		http.Error(w, "cross-site login refused", http.StatusForbidden)
		return
	}
	if d := s.auth.lockedFor(ip); d > 0 {
		w.WriteHeader(http.StatusTooManyRequests)
		s.pages.renderLogin(w, loginData{Next: next, Error: "Too many failed attempts. Try again in " + fmtDuration(int64(d.Seconds()+1)) + "."})
		return
	}
	if !s.auth.checkPassword(r.PostFormValue("password")) {
		s.auth.recordFailure(ip)
		log.Printf("login failed from %s", ip)
		w.WriteHeader(http.StatusUnauthorized)
		s.pages.renderLogin(w, loginData{Next: next, Error: "Wrong password."})
		return
	}
	s.auth.clearFailures(ip)
	sess := s.auth.newSession()
	setSessionCookie(w, r, sess.id, int(sessionMaxAge.Seconds()))
	log.Printf("login from %s", ip)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.drop(sessionFrom(r))
	setSessionCookie(w, r, "", -1)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// safeNext only allows local paths as a post-login redirect target.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := s.Status(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("writing status: %v", err)
	}
}

// Status queries the daemon and builds the overview status. It never fails:
// an unreachable daemon is reported in the response body.
func (s *Server) Status(ctx context.Context) StatusResponse {
	resp := StatusResponse{
		FetchedAt:      s.now().UTC(),
		RefreshSeconds: s.refresh.Seconds(),
		Warnings:       []status.Warning{},
	}
	info, err := s.rpc.GetInfo(ctx)
	if err != nil {
		resp.Error = err.Error()
		resp.Warnings = append(resp.Warnings, status.Warning{
			Level: "error", Code: "unreachable", Message: "Cannot reach monerod. " + errMessage(err),
		})
		return resp
	}
	// The last block header only feeds the "last block age" figure, so a
	// failure here degrades the view rather than failing it.
	last, err := s.rpc.GetLastBlockHeader(ctx)
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
