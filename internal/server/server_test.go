package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc/rpctest"
)

const testPassword = "correct horse battery staple"

// env is a dashboard wired to a fake monerod serving recorded fixtures.
type env struct {
	t      *testing.T
	daemon *rpctest.Daemon
	srv    *Server
	http   *httptest.Server
	client *http.Client
	csrf   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := rpctest.New(t)
	s := New(testNode(t, d), Config{Refresh: 5 * time.Second, Password: testPassword})
	h := httptest.NewServer(s.Handler())
	t.Cleanup(h.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &env{t: t, daemon: d, srv: s, http: h, client: c}
}

// testNode is a cached node refreshing quickly, with its loop running for
// the duration of the test.
func testNode(t *testing.T, d *rpctest.Daemon) *rpc.Node {
	n := rpc.NewNode(rpc.New(rpc.Options{URL: d.URL}), 50*time.Millisecond, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go n.Run(ctx)
	return n
}

func (e *env) do(req *http.Request) (*http.Response, string) {
	e.t.Helper()
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func (e *env) get(path string, headers ...string) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.http.URL+path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return e.do(req)
}

// post submits a form from the dashboard's own origin.
func (e *env) post(path string, form url.Values, headers ...string) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.http.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", e.http.URL)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return e.do(req)
}

var csrfRE = regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`)

// login signs in and remembers the CSRF token from the first page.
func (e *env) login() {
	e.t.Helper()
	resp, _ := e.post("/login", url.Values{"password": {testPassword}, "next": {"/"}})
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("login: status %d", resp.StatusCode)
	}
	_, body := e.get("/")
	m := csrfRE.FindStringSubmatch(body)
	if m == nil {
		e.t.Fatal("no CSRF token on the overview page")
	}
	e.csrf = m[1]
}

// action posts an action the way htmx does.
func (e *env) action(name string, form url.Values) (*http.Response, string) {
	e.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf", e.csrf)
	return e.post("/actions/"+name, form, "HX-Request", "true")
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("response is missing %q", w)
		}
	}
}

// ---- Authentication ----

func TestLoginRequired(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.get("/peers")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login?next=%2Fpeers" {
		t.Errorf("page: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := e.get("/api/status"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("api: %d", resp.StatusCode)
	}
	resp, _ = e.get("/mempool", "HX-Request", "true")
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("HX-Redirect") != "/login" {
		t.Errorf("htmx: %d %q", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}
	if resp, _ := e.post("/actions/stop_daemon", url.Values{"confirm": {"stop"}}); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("action: %d", resp.StatusCode)
	}
	if len(e.daemon.Calls("stop_daemon")) != 0 {
		t.Fatal("an unauthenticated request reached the daemon")
	}
	// Public: the login page, health check and assets.
	for _, p := range []string{"/login", "/healthz", "/style.css", "/app.js", "/favicon.svg", "/vendor/htmx-2.0.11.min.js"} {
		if resp, _ := e.get(p); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
}

func TestLoginFlow(t *testing.T) {
	e := newEnv(t)
	resp, body := e.post("/login", url.Values{"password": {"wrong"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", resp.StatusCode)
	}
	mustContain(t, body, "Wrong password.")

	resp, _ = e.post("/login", url.Values{"password": {testPassword}, "next": {"/mempool?x=1"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/mempool?x=1" {
		t.Fatalf("login: %d -> %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %+v", cookie)
	}
	if resp, _ := e.get("/peers"); resp.StatusCode != http.StatusOK {
		t.Fatalf("after login: %d", resp.StatusCode)
	}

	// Log out ends the session.
	_, body = e.get("/")
	csrf := csrfRE.FindStringSubmatch(body)[1]
	e.post("/logout", url.Values{"csrf": {csrf}})
	if resp, _ := e.get("/peers"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("after logout: %d", resp.StatusCode)
	}
}

func TestLoginRedirectStaysLocal(t *testing.T) {
	for _, next := range []string{"https://evil.example/", "//evil.example/", "/\\evil.example", "javascript:alert(1)"} {
		if got := safeNext(next); got != "/" {
			t.Errorf("safeNext(%q) = %q", next, got)
		}
	}
	if got := safeNext("/block/5"); got != "/block/5" {
		t.Errorf("safeNext(/block/5) = %q", got)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	now := time.Unix(1_800_000_000, 0)
	e.srv.now = func() time.Time { return now }
	for i := 0; i < loginFreeTries; i++ {
		if resp, _ := e.post("/login", url.Values{"password": {"nope"}}); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, resp.StatusCode)
		}
	}
	e.post("/login", url.Values{"password": {"nope"}}) // first failure past the free tries: locked
	resp, body := e.post("/login", url.Values{"password": {testPassword}})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("locked out: %d", resp.StatusCode)
	}
	mustContain(t, body, "Too many failed attempts")
	now = now.Add(2 * time.Second)
	if resp, _ := e.post("/login", url.Values{"password": {testPassword}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("after lockout: %d", resp.StatusCode)
	}
}

func TestSessionExpires(t *testing.T) {
	e := newEnv(t)
	now := time.Unix(1_800_000_000, 0)
	e.srv.now = func() time.Time { return now }
	e.login()
	now = now.Add(sessionIdle + time.Minute)
	if resp, _ := e.get("/"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("idle session still valid: %d", resp.StatusCode)
	}
}

func TestCSRF(t *testing.T) {
	e := newEnv(t)
	e.login()
	form := url.Values{"host": {"1.2.3.4"}, "seconds": {"60"}}
	if resp, _ := e.post("/actions/ban", form); resp.StatusCode != http.StatusForbidden {
		t.Errorf("no token: %d", resp.StatusCode)
	}
	form.Set("csrf", e.csrf)
	if resp, _ := e.post("/actions/ban", form, "Origin", "https://evil.example"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross origin: %d", resp.StatusCode)
	}
	if len(e.daemon.Calls("set_bans")) != 0 {
		t.Fatal("a forged request reached the daemon")
	}
	if resp, _ := e.post("/actions/ban", url.Values{"host": {"1.2.3.4"}, "seconds": {"60"}}, "X-CSRF-Token", e.csrf); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("header token: %d", resp.StatusCode)
	}
	if len(e.daemon.Calls("set_bans")) != 1 {
		t.Fatal("valid request did not reach the daemon")
	}
}

func TestNoAuthMode(t *testing.T) {
	d := rpctest.New(t)
	s := New(testNode(t, d), Config{NoAuth: true})
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	resp, err := http.Get(h.URL + "/peers")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no-auth page: %d", resp.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.get("/login")
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-eval") || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("headers: CSP=%q XFO=%q", csp, resp.Header.Get("X-Frame-Options"))
	}
}

// ---- Pages ----

func TestPagesRenderFixtureData(t *testing.T) {
	e := newEnv(t)
	e.login()
	pages := map[string][]string{
		"/":            {"Synchronized", `<dd id="height">131</dd>`, "fakechain", "v0.18.3.1-unknown", "Votes", "of the space available to it"},
		"/peers":       {"127.0.0.1:38090", "10.9.8.7", "192.168.50.0/24", "Unban", "unlimited incoming"},
		"/network":     {"8,192", "2,048", "Synchronisation", "127.0.0.1:55448"},
		"/mempool":     {"f7e227d5…8ce43980", "0.0026052 XMR", "Remove selected", "1,202,770 pXMR/B"},
		"/blocks":      {`href="/block/130"`, "Chain height 131", "Older →"},
		"/block/100":   {"Block 100", "275e60f4a94a1719c10b76acc92cb52c26c00c069cf1818aaea54ac98d72f433", "Decoded block"},
		"/mining":      {"Not mining", "RandomX", "Generate blocks"},
		"/maintenance": {"Not pruned", "Stop daemon", "Pop blocks"},
		"/tools":       {"Broadcast a transaction", "1,200,000 pXMR/B"},
		"/console":     {"console-methods", "get_txpool_backlog"},
		"/tx/f7e227d5f052446c0f6c71585c44837a27cead91915626618cbca2f28ce43980": {"in pool", "ring size 16", "Relay again"},
	}
	for path, wants := range pages {
		resp, body := e.get(path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", path, resp.StatusCode)
			continue
		}
		for _, w := range wants {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", path, w)
			}
		}
	}
}

func TestLivePartial(t *testing.T) {
	e := newEnv(t)
	e.login()
	resp, body := e.get("/peers", "HX-Request", "true", "HX-Target", "live")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if strings.Contains(body, "<html") || strings.Contains(body, "Ban a host") {
		t.Error("partial response contains more than the live section")
	}
	mustContain(t, body, "127.0.0.1:38090", `hx-swap-oob="true"`, "fakechain")
}

func TestToolResults(t *testing.T) {
	e := newEnv(t)
	e.login()
	_, body := e.get("/tools?op=histogram&amounts=0")
	mustContain(t, body, "RingCT", ">130<")
	_, body = e.get("/tools?op=txids_loose&txid_template=" + strings.Repeat("0", 64) + "&num_matching_bits=8")
	mustContain(t, body, "Not available")
	_, body = e.get("/tools?op=key_images&key_images=nothex")
	mustContain(t, body, "is not a 64-character hex key image")
	_, body = e.post("/console", url.Values{"csrf": {e.csrf}, "endpoint": {"/json_rpc"}, "method": {"get_info"}, "params": {"{}"}})
	mustContain(t, body, "&#34;nettype&#34;: &#34;fakechain&#34;")
	_, body = e.post("/console", url.Values{"csrf": {e.csrf}, "endpoint": {"/get_blocks.bin"}})
	mustContain(t, body, "binary .bin endpoints are not supported")
}

func TestSearch(t *testing.T) {
	e := newEnv(t)
	e.login()
	// Like monerod, only know the block hashes that exist.
	const block100 = "845469019f042b6be9892b8ab63fafdb2de98a0d2f977f68225733bc2ecc785c"
	e.daemon.Handle("get_block_header_by_hash", func(p json.RawMessage) any {
		if !strings.Contains(string(p), block100) {
			return &rpctest.RPCError{Code: -5, Message: "Internal error: can't get block by hash"}
		}
		return map[string]any{"status": "OK", "block_header": map[string]any{"hash": block100, "height": 100}}
	})
	for q, want := range map[string]string{
		"100": "/block/100",
		"f7e227d5f052446c0f6c71585c44837a27cead91915626618cbca2f28ce43980": "/tx/f7e227d5f052446c0f6c71585c44837a27cead91915626618cbca2f28ce43980",
		"banana": "/blocks",
		block100: "/block/" + block100,
	} {
		resp, _ := e.get("/search?q=" + q)
		if loc := resp.Header.Get("Location"); loc != want {
			t.Errorf("search %q -> %q, want %q", q, loc, want)
		}
	}
}

// A daemon that was never reachable: nothing to show.
func TestUnreachableDaemon(t *testing.T) {
	e := newEnv(t)
	e.daemon.Close()
	e.login()
	resp, body := e.get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	mustContain(t, body, "Unreachable", "Cannot reach monerod", `<section class="grid stale">`, `badge err">unreachable`)
	var st StatusResponse
	_, raw := e.get("/api/status")
	if json.Unmarshal([]byte(raw), &st) != nil || st.OK {
		t.Errorf("api status = %s", raw)
	}
}

// A daemon that goes away: the last good data stays up, flagged with its age.
func TestLastGoodDataWhenDaemonGoesAway(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.get("/peers")
	e.daemon.Close()
	var body string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, body = e.get("/"); strings.Contains(body, "stale-flag") {
			break
		}
	}
	mustContain(t, body,
		`<dd id="height">131</dd>`, // last known data, not blanks
		"stale-flag", "old</span>", "monerod is not answering",
		"Showing data from", `<section class="grid stale">`)
	if strings.Contains(body, "Unreachable") {
		t.Error("last good data replaced by the unreachable state")
	}
	_, body = e.get("/peers")
	mustContain(t, body, "127.0.0.1:38090", "stale-flag")

	var st StatusResponse
	_, raw := e.get("/api/status")
	if json.Unmarshal([]byte(raw), &st) != nil || !st.OK || !st.Stale || st.DataAsOf == nil {
		t.Errorf("api status = %s", raw)
	}
}

// Fresh data carries no flag.
func TestNoStaleFlagWhenFresh(t *testing.T) {
	e := newEnv(t)
	e.login()
	_, body := e.get("/")
	if strings.Contains(body, "stale-flag") || strings.Contains(body, "Showing data from") {
		t.Error("fresh data flagged stale")
	}
}

func TestDaemonDataIsEscaped(t *testing.T) {
	e := newEnv(t)
	e.login()
	evil := `<script>alert(1)</script>`
	e.daemon.Handle("get_bans", func(json.RawMessage) any {
		return map[string]any{"status": "OK", "bans": []map[string]any{{"host": evil, "seconds": 5}}}
	})
	_, body := e.get("/peers")
	if strings.Contains(body, evil) {
		t.Fatal("daemon data was not escaped")
	}
	mustContain(t, body, "&lt;script&gt;")
}

// ---- Actions ----

func TestActionBan(t *testing.T) {
	e := newEnv(t)
	e.login()
	resp, body := e.action("ban", url.Values{"host": {"203.0.113.0/24"}, "seconds": {"86400"}})
	if resp.StatusCode != http.StatusOK || resp.Header.Get("HX-Trigger") != "live-refresh" {
		t.Fatalf("status %d trigger %q", resp.StatusCode, resp.Header.Get("HX-Trigger"))
	}
	mustContain(t, body, `class="toast ok"`, "Banned 203.0.113.0/24 for 1d 0h.")
	calls := e.daemon.Calls("set_bans")
	if len(calls) != 1 || !strings.Contains(string(calls[0].Params), `"host":"203.0.113.0/24","ban":true,"seconds":86400`) {
		t.Fatalf("set_bans calls = %+v", calls)
	}
}

func TestActionValidation(t *testing.T) {
	e := newEnv(t)
	e.login()
	cases := []struct {
		action string
		form   url.Values
		want   string
		method string // must not be called
	}{
		{"ban", url.Values{"host": {"not-an-ip"}, "seconds": {"60"}}, "is not an IP address or subnet", "set_bans"},
		{"stop_daemon", url.Values{"confirm": {"yes"}}, `type &#34;stop&#34; to confirm`, "stop_daemon"},
		{"prune_blockchain", url.Values{}, `type &#34;prune&#34; to confirm`, "prune_blockchain"},
		{"pop_blocks", url.Values{"count": {"10"}, "confirm": {"1"}}, "type the number of blocks (10) to confirm", "pop_blocks"},
		{"flush_txpool", url.Values{}, "select transactions to remove", "flush_txpool"},
		{"relay_tx", url.Values{"txid": {"abc"}}, "is not a transaction hash", "relay_tx"},
		{"start_mining", url.Values{"address": {"short"}, "threads": {"1"}}, "valid Monero address", "start_mining"},
		{"set_limit", url.Values{"limit_down": {"0"}, "limit_up": {"5"}}, "at least 1 kB/s", "set_limit"},
		{"set_log_level", url.Values{"level": {"9"}}, "level must be a whole number up to 4", "set_log_level"},
		{"set_log_categories", url.Values{"categories": {"x; rm -rf"}}, "categories look like", "set_log_categories"},
		{"submit_block", url.Values{"blob": {"zz"}}, "must be hex", "submit_block"},
	}
	for _, c := range cases {
		_, body := e.action(c.action, c.form)
		if !strings.Contains(body, `class="toast error"`) || !strings.Contains(body, c.want) {
			t.Errorf("%s: body %q, want %q", c.action, body, c.want)
		}
		if n := len(e.daemon.Calls(c.method)); n != 0 {
			t.Errorf("%s: invalid input reached the daemon (%d calls)", c.action, n)
		}
	}
}

func TestActionsReachDaemon(t *testing.T) {
	e := newEnv(t)
	e.login()
	addr := "44AFFq5kSiGBoZ4NMDwYtN18obc8AemS33DBLWs3H7otXft3XjrpDtQGv7SqSsaBYBb98uNbr2VBBEt7f2wfn3RVGQBEP3A"
	tx := "f7e227d5f052446c0f6c71585c44837a27cead91915626618cbca2f28ce43980"
	cases := []struct {
		action, method string
		form           url.Values
		params         string // substring of the params sent
	}{
		{"unban", "set_bans", url.Values{"host": {"10.9.8.7"}}, `"ban":false`},
		{"out_peers", "out_peers", url.Values{"limit": {"16"}}, `"out_peers":16`},
		{"in_peers", "in_peers", url.Values{"limit": {""}}, `"in_peers":4294967295`},
		{"set_limit", "set_limit", url.Values{"reset": {"1"}}, `"limit_down":-1,"limit_up":-1`},
		{"set_limit", "set_limit", url.Values{"limit_down": {"1024"}, "limit_up": {"512"}}, `"limit_down":1024,"limit_up":512`},
		{"flush_txpool", "flush_txpool", url.Values{"txid": {tx}}, tx},
		{"flush_txpool", "flush_txpool", url.Values{"confirm": {"flush"}}, `"txids":[]`},
		{"relay_tx", "relay_tx", url.Values{"txid": {tx}}, tx},
		{"start_mining", "start_mining", url.Values{"address": {addr}, "threads": {"2"}, "background": {"1"}}, `"threads_count":2,"do_background_mining":true`},
		{"stop_mining", "stop_mining", nil, ""},
		{"set_log_hash_rate", "set_log_hash_rate", url.Values{"visible": {"1"}}, `"visible":true`},
		{"generate_blocks", "generateblocks", url.Values{"address": {addr}, "count": {"10"}}, `"amount_of_blocks":10`},
		{"submit_block", "submit_block", url.Values{"blob": {"0a0b"}}, `["0a0b"]`},
		{"set_log_level", "set_log_level", url.Values{"level": {"2"}}, `"level":2`},
		{"set_log_categories", "set_log_categories", url.Values{"categories": {"*:WARNING,net.p2p:DEBUG"}}, `net.p2p:DEBUG`},
		{"save_bc", "save_bc", nil, ""},
		{"flush_cache", "flush_cache", url.Values{"bad_txs": {"1"}}, `"bad_blocks":false,"bad_txs":true`},
		{"prune_blockchain", "prune_blockchain", url.Values{"confirm": {"prune"}}, `"check":false`},
		{"pop_blocks", "pop_blocks", url.Values{"count": {"10"}, "confirm": {"10"}}, `"nblocks":10`},
		{"update_check", "update", nil, `"command":"check"`},
		{"stop_daemon", "stop_daemon", url.Values{"confirm": {"stop"}}, ""},
	}
	for _, c := range cases {
		before := len(e.daemon.Calls(c.method))
		_, body := e.action(c.action, c.form)
		calls := e.daemon.Calls(c.method)
		if len(calls) != before+1 {
			t.Errorf("%s: %s not called (body %q)", c.action, c.method, body)
			continue
		}
		if !strings.Contains(string(calls[len(calls)-1].Params), c.params) {
			t.Errorf("%s: params %s, want %s", c.action, calls[len(calls)-1].Params, c.params)
		}
	}
}

// Without JavaScript, an action redirects back and the result shows as a
// flash message on the next page.
func TestActionWithoutJavaScript(t *testing.T) {
	e := newEnv(t)
	e.login()
	resp, _ := e.post("/actions/save_bc", url.Values{"csrf": {e.csrf}, "return": {"/maintenance"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/maintenance" {
		t.Fatalf("status %d -> %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, body := e.get("/maintenance")
	mustContain(t, body, `class="toast ok"`, "Blockchain saved to disk.")
	_, body = e.get("/maintenance")
	if strings.Contains(body, "Blockchain saved to disk.") {
		t.Error("flash message shown twice")
	}
}

func TestActionDaemonError(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.daemon.Handle("set_log_hash_rate", func(json.RawMessage) any { return map[string]string{"status": "NOT MINING"} })
	_, body := e.action("set_log_hash_rate", url.Values{"visible": {"1"}})
	mustContain(t, body, `class="toast error"`, "monerod answered: NOT MINING")
}

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

func TestErrMessageTimeoutAndRefused(t *testing.T) {
	d := rpctest.New(t)
	d.Handle("update", func(json.RawMessage) any { time.Sleep(200 * time.Millisecond); return nil })
	c := rpc.New(rpc.Options{URL: d.URL, Timeout: 50 * time.Millisecond})
	_, err := c.Update(context.Background(), "check")
	if got := errMessage(err); !strings.Contains(got, "did not answer in time") {
		t.Errorf("timeout: %q", got)
	}
	d.Handle("get_info", func(json.RawMessage) any { panic(http.ErrAbortHandler) }) // drops the connection
	_, err = rpc.New(rpc.Options{URL: d.URL}).GetInfo(context.Background())
	if got := errMessage(err); !strings.Contains(got, "closed the connection") {
		t.Errorf("reset: %q", got)
	}
	d.Close()
	_, err = rpc.New(rpc.Options{URL: d.URL}).GetInfo(context.Background())
	if got := errMessage(err); !strings.Contains(got, "connection refused") {
		t.Errorf("refused: %q", got)
	}
}

func TestAuthSweep(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	a := newAuth("pw", false, func() time.Time { return now })
	a.newSession()
	a.recordFailure("192.0.2.1")
	now = now.Add(sessionMaxAge + 2*time.Hour)
	a.newSession() // sweeps
	if len(a.sessions) != 1 || len(a.failures) != 0 {
		t.Fatalf("after sweep: %d sessions, %d failure records", len(a.sessions), len(a.failures))
	}
}

// Pages are served from the cache: repeated loads don't hit the daemon,
// and an action makes the next load fetch fresh data.
func TestPagesServedFromCache(t *testing.T) {
	d := rpctest.New(t)
	n := rpc.NewNode(rpc.New(rpc.Options{URL: d.URL}), time.Hour, time.Hour) // no background refreshes during the test
	s := New(n, Config{Password: testPassword})
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	jar, _ := cookiejar.New(nil)
	e := &env{t: t, daemon: d, srv: s, http: h, client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	e.login()

	e.get("/peers")
	before := len(d.Calls("get_connections"))
	for i := 0; i < 5; i++ {
		e.get("/peers")
	}
	if got := len(d.Calls("get_connections")); got != before {
		t.Fatalf("get_connections called %d more times; want 0 (cached)", got-before)
	}
	e.action("ban", url.Values{"host": {"1.2.3.4"}, "seconds": {"60"}})
	e.get("/peers")
	if got := len(d.Calls("get_bans")); got < 2 {
		t.Fatalf("get_bans not refetched after an action (%d calls)", got)
	}
}

func TestStaleReasonBusy(t *testing.T) {
	got := staleReason(&rpc.StatusError{Method: "get_fee_estimate", Status: "BUSY"})
	if !strings.Contains(got, "busy") || strings.Contains(got, "not answering") {
		t.Errorf("BUSY reason = %q", got)
	}
}
