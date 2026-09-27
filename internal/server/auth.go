package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookie   = "mdash_session"
	sessionIdle     = 12 * time.Hour
	sessionMaxAge   = 7 * 24 * time.Hour
	loginFreeTries  = 5
	loginMaxLockout = 15 * time.Minute
)

// session is a logged-in browser. The whole dashboard requires one.
type session struct {
	id       string
	csrf     string
	created  time.Time
	lastSeen time.Time
	flashes  []flash
}

// flash is a message shown once on the next page render (used when an
// action is submitted without JavaScript and answered with a redirect).
type flash struct {
	Level   string // "ok" or "error"
	Message string
}

type auth struct {
	passwordHash [32]byte
	disabled     bool
	now          func() time.Time

	mu       sync.Mutex
	sessions map[string]*session
	failures map[string]*loginFailures
}

type loginFailures struct {
	count       int
	last        time.Time
	lockedUntil time.Time
}

func newAuth(password string, disabled bool, now func() time.Time) *auth {
	return &auth{
		passwordHash: sha256.Sum256([]byte(password)),
		disabled:     disabled,
		now:          now,
		sessions:     map[string]*session{},
		failures:     map[string]*loginFailures{},
	}
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// checkPassword compares in constant time; hashing first also hides the
// password's length.
func (a *auth) checkPassword(pw string) bool {
	h := sha256.Sum256([]byte(pw))
	return subtle.ConstantTimeCompare(h[:], a.passwordHash[:]) == 1
}

// lockedFor reports how long logins from ip are still refused.
func (a *auth) lockedFor(ip string) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	if f := a.failures[ip]; f != nil {
		if d := f.lockedUntil.Sub(a.now()); d > 0 {
			return d
		}
	}
	return 0
}

// recordFailure counts a failed login. After loginFreeTries, each further
// failure locks the IP out for an exponentially growing time.
func (a *auth) recordFailure(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.failures[ip]
	if f == nil {
		f = &loginFailures{}
		a.failures[ip] = f
	}
	f.count++
	f.last = a.now()
	if len(a.failures) > 10000 {
		a.sweep(f.last)
	}
	if extra := f.count - loginFreeTries; extra > 0 {
		d := time.Second << min(extra-1, 20)
		f.lockedUntil = a.now().Add(min(d, loginMaxLockout))
	}
}

func (a *auth) clearFailures(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, ip)
}

func (a *auth) newSession() *session {
	now := a.now()
	s := &session{id: randomToken(), csrf: randomToken(), created: now, lastSeen: now}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweep(now)
	a.sessions[s.id] = s
	return s
}

// sweep drops expired sessions and stale login failures so neither map
// grows without bound. Callers hold a.mu.
func (a *auth) sweep(now time.Time) {
	for id, s := range a.sessions {
		if now.Sub(s.lastSeen) > sessionIdle || now.Sub(s.created) > sessionMaxAge {
			delete(a.sessions, id)
		}
	}
	for ip, f := range a.failures {
		if now.Sub(f.lockedUntil) > time.Hour && now.Sub(f.last) > time.Hour {
			delete(a.failures, ip)
		}
	}
}

// lookup returns the live session for the request, refreshing its idle
// timer, or nil.
func (a *auth) lookup(r *http.Request) *session {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[c.Value]
	if s == nil {
		return nil
	}
	if now.Sub(s.lastSeen) > sessionIdle || now.Sub(s.created) > sessionMaxAge {
		delete(a.sessions, s.id)
		return nil
	}
	s.lastSeen = now
	return s
}

func (a *auth) drop(s *session) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, s.id)
}

func (a *auth) addFlash(s *session, level, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s.flashes = append(s.flashes, flash{Level: level, Message: msg})
}

func (a *auth) takeFlashes(s *session) []flash {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := s.flashes
	s.flashes = nil
	return f
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sameOrigin rejects cross-site POSTs. SameSite=Strict cookies already stop
// most of them; this also covers browsers that send an Origin header for a
// same-site but cross-origin request.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		// Older browsers omit Origin on same-origin form posts; fall back to
		// Referer when present.
		ref := r.Header.Get("Referer")
		if ref == "" {
			return origin == ""
		}
		origin = ref
	}
	for _, scheme := range []string{"http://", "https://"} {
		if len(origin) >= len(scheme)+len(r.Host) && origin[:len(scheme)] == scheme {
			rest := origin[len(scheme):]
			if rest == r.Host || (len(rest) > len(r.Host) && rest[:len(r.Host)] == r.Host && rest[len(r.Host)] == '/') {
				return true
			}
		}
	}
	return false
}
