package server

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"math/big"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
)

//go:embed web
var webFS embed.FS

//go:embed templates
var templateFS embed.FS

// pageSet holds one template set per page: the shared layout and partials
// plus that page's own "page" and "live" templates.
type pageSet struct {
	pages map[string]*template.Template
	login *template.Template
}

func mustLoadPages() *pageSet {
	base := template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/partials.html"))
	names, err := fs.Glob(templateFS, "templates/pages/*.html")
	if err != nil {
		panic(err)
	}
	ps := &pageSet{pages: map[string]*template.Template{}}
	for _, n := range names {
		t := template.Must(template.Must(base.Clone()).ParseFS(templateFS, n))
		ps.pages[strings.TrimSuffix(path.Base(n), ".html")] = t
	}
	ps.login = template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/login.html"))
	return ps
}

// view is what every page template receives.
type view struct {
	Title   string
	Nav     string // active nav tab
	Path    string // current request URI, for live refreshes
	CSRF    string
	Flashes []flash
	Header  headerView
	// Live marks pages with an auto-refreshing #live section.
	Live           bool
	RefreshSeconds float64
	Data           any
}

// headerView feeds the header badges on every page. It is refreshed
// out-of-band with each live update.
type headerView struct {
	OK         bool
	Nettype    string
	Restricted bool
	Version    string
	Updated    time.Time // when the oldest data on the page was fetched
	// Stale flags data that is out of date: Age is how old, Why the reason.
	Stale bool
	Age   string
	Why   string
}

var navItems = []struct{ Key, Label, Href string }{
	{"overview", "Overview", "/"},
	{"peers", "Peers", "/peers"},
	{"network", "Network", "/network"},
	{"mempool", "Mempool", "/mempool"},
	{"blocks", "Blocks", "/blocks"},
	{"mining", "Mining", "/mining"},
	{"maintenance", "Maintenance", "/maintenance"},
	{"tools", "Tools", "/tools"},
	{"console", "RPC console", "/console"},
}

type loginData struct {
	Next  string
	Error string
}

func (ps *pageSet) renderLogin(w http.ResponseWriter, d loginData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := ps.login.ExecuteTemplate(w, "login", d); err != nil {
		log.Printf("rendering login: %v", err)
	}
}

// render writes a full page, or only its live section (plus out-of-band
// header updates) when htmx asks for #live.
func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, v view) {
	t := s.pages.pages[page]
	if t == nil {
		http.Error(w, "unknown page "+page, http.StatusInternalServerError)
		return
	}
	sess := sessionFrom(r)
	v.Nav = page
	v.Path = r.URL.RequestURI()
	v.RefreshSeconds = s.refresh.Seconds()
	if sess != nil {
		v.CSRF = sess.csrf
		v.Flashes = s.auth.takeFlashes(sess)
	}
	v.Header = s.header(r)

	name := "layout"
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "live" {
		name = "live-response"
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, v); err != nil {
		log.Printf("rendering %s: %v", page, err)
		http.Error(w, "internal error rendering page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes())
}

// renderFragment executes one named template of a page on its own, e.g.
// the next batch of a long table.
func (s *Server) renderFragment(w http.ResponseWriter, page, name string, data any) {
	var buf bytes.Buffer
	if err := s.pages.pages[page].ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("rendering %s/%s: %v", page, name, err)
		http.Error(w, "internal error rendering rows", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes())
}

// header fetches the node identity shown in the header.
func (s *Server) header(r *http.Request) headerView {
	h := headerView{Updated: s.now()}
	if info, err := s.rpc.GetInfo(r.Context()); err == nil {
		h.OK = true
		h.Nettype = info.Nettype
		h.Restricted = info.Restricted
		h.Version = info.Version
	}
	// Everything the page read from the cache (this call included) has
	// reported its age by now.
	if f := rpc.FreshnessFrom(r.Context()); f != nil {
		if at := f.Oldest(); !at.IsZero() {
			h.Updated = at
		}
		if stale, err := f.Stale(); stale && h.OK {
			age := s.now().Sub(h.Updated)
			h.Stale, h.Age = true, fmtDuration(int64(max(age, 0).Seconds()))
			h.Why = "Refreshes from monerod are running late."
			if err != nil {
				h.Why = "monerod is not answering: " + errMessage(err)
			}
		}
	}
	return h
}

// result pairs a value with the error from fetching it, so one failing RPC
// call only blanks its own section of a page.
type result[T any] struct {
	V   T
	Err error
}

func try[T any](v T, err error) result[T] { return result[T]{V: v, Err: err} }

var funcs = template.FuncMap{
	"nav":      func() any { return navItems },
	"int":      func(v any) string { return fmtIntAny(v) },
	"bytes":    func(v any) string { return fmtBytes(toUint(v)) },
	"hashrate": func(v any) string { return fmtHashrate(toFloat(v)) },
	"dur":      func(v any) string { return fmtDuration(int64(toUint(v))) },
	"xmr":      fmtXMR,
	"widexmr":  fmtWideXMR,
	"feerate":  func(fee, weight uint64) string { return fmtFeeRate(fee, weight) },
	"bigint":   fmtBigInt,
	"widehex":  fmtWideHex,
	"short":    shortHash,
	"ts":       timeTag,
	"ago":      agoTag,
	"pct":      func(a, b any) string { return fmtPct(toFloat(a), toFloat(b)) },
	"barw":     barWidth,
	"barh":     barHeight,
	"errmsg":   errMessage,
	"unsupported": func(err error) bool {
		return errors.Is(err, rpc.ErrUnsupported)
	},
	"pretty": rpc.PrettyJSON,
	"add":    func(a, b uint64) uint64 { return a + b },
	"div": func(a uint64, b int64) uint64 {
		if b <= 0 {
			return 0
		}
		return a / uint64(b)
	},
	"sub64":   func(a, b int64) int64 { return a - b },
	"halflen": func(n int) uint64 { return uint64(n / 2) },
	"sub":     func(a, b uint64) uint64 { return a - min(a, b) },
	"version": fmtDaemonVersion,
	"limit":   limitText,
	"feelabel": func(i int) string {
		if labels := []string{"Low", "Normal", "Elevated", "Priority"}; i < len(labels) {
			return labels[i]
		}
		return "Tier " + strconv.Itoa(i+1)
	},
	"yesno": func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	},
	"dict": func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	},
	"min100": func(n int) int { return min(n, rowsPerPage) },
	"seq": func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	},
}

func toUint(v any) uint64 {
	switch n := v.(type) {
	case uint64:
		return n
	case uint32:
		return uint64(n)
	case uint16:
		return uint64(n)
	case uint8:
		return uint64(n)
	case int:
		return uint64(max(n, 0))
	case int64:
		return uint64(max(n, 0))
	case float64:
		return uint64(max(n, 0))
	}
	return 0
}

func toFloat(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	if n, ok := v.(int64); ok {
		return float64(n)
	}
	return float64(toUint(v))
}

func fmtIntAny(v any) string {
	if n, ok := v.(int64); ok && n < 0 {
		return "-" + fmtInt(uint64(-n))
	}
	if n, ok := v.(int); ok && n < 0 {
		return "-" + fmtInt(uint64(-n))
	}
	return fmtInt(toUint(v))
}

// fmtXMR formats atomic units (1e-12 XMR) without floating point error,
// trimming trailing zeros.
func fmtXMR(atomic uint64) string {
	whole, frac := atomic/1e12, atomic%1e12
	s := fmtInt(whole)
	if frac != 0 {
		s += "." + strings.TrimRight(fmt.Sprintf("%012d", frac), "0")
	}
	return s + " XMR"
}

// fmtWideXMR formats a 128-bit hex amount of atomic units.
func fmtWideXMR(hexAmount string) string {
	n, ok := new(big.Int).SetString(strings.TrimPrefix(hexAmount, "0x"), 16)
	if !ok {
		return dash
	}
	whole, frac := new(big.Int).QuoRem(n, big.NewInt(1e12), new(big.Int))
	s := groupThousands(whole.String())
	if frac.Sign() != 0 {
		s += "." + strings.TrimRight(fmt.Sprintf("%012d", frac.Uint64()), "0")
	}
	return s + " XMR"
}

func fmtWideHex(hexValue string) string {
	n, ok := new(big.Int).SetString(strings.TrimPrefix(hexValue, "0x"), 16)
	if !ok {
		return dash
	}
	return groupThousands(n.String())
}

// fmtFeeRate is piconero per byte of weight.
func fmtFeeRate(fee, weight uint64) string {
	if weight == 0 {
		return dash
	}
	return fmtInt(fee/weight) + " pXMR/B"
}

func fmtPct(a, b float64) string {
	if b == 0 {
		return dash
	}
	return toFixed(a/b*100, 1) + "%"
}

// barHeight is barWidth for vertical bars.
func barHeight(a, b any) template.CSS {
	return template.CSS(strings.Replace(string(barWidth(a, b)), "width", "height", 1))
}

// barWidth is a CSS width for a bar showing a out of b.
func barWidth(a, b any) template.CSS {
	fa, fb := toFloat(a), toFloat(b)
	if fb <= 0 {
		return "width: 0%"
	}
	return template.CSS("width: " + strconv.FormatFloat(min(100, fa/fb*100), 'f', 2, 64) + "%")
}

func fmtDaemonVersion(v uint32) string {
	return fmt.Sprintf("%d.%d", v>>16, v&0xffff)
}

func shortHash(h string) string {
	if len(h) <= 16 {
		return h
	}
	return h[:8] + "…" + h[len(h)-8:]
}

// timeTag renders a unix time as a <time> element in UTC; app.js rewrites it
// in the viewer's local time.
func timeTag(unix int64) template.HTML {
	if unix <= 0 {
		return dash
	}
	t := time.Unix(unix, 0).UTC()
	return template.HTML(fmt.Sprintf(`<time datetime="%s" data-local>%s</time>`,
		t.Format(time.RFC3339), t.Format("2006-01-02 15:04:05")+" UTC"))
}

func agoTag(unix int64) template.HTML {
	if unix <= 0 {
		return dash
	}
	t := time.Unix(unix, 0).UTC()
	age := int64(time.Since(t).Seconds())
	return template.HTML(fmt.Sprintf(`<time datetime="%s" title="%s">%s ago</time>`,
		t.Format(time.RFC3339), t.Format("2006-01-02 15:04:05")+" UTC", fmtDuration(max(age, 0))))
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

// errMessage explains a failed section to the operator.
func errMessage(err error) string {
	var se *rpc.StatusError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, rpc.ErrUnsupported):
		return "Not available: this needs the unrestricted RPC port, or a newer monerod."
	case errors.As(err, &se):
		return "monerod answered: " + se.Status
	case isTimeout(err):
		return "monerod did not answer in time. It may be busy (a large prune or a slow lookup), or unreachable."
	case errors.Is(err, syscall.ECONNREFUSED):
		return "Cannot reach monerod: connection refused. Is it running?"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "monerod closed the connection. It may be shutting down or restarting."
	}
	return err.Error()
}
