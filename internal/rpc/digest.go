package rpc

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// digestTransport implements HTTP digest authentication (RFC 7616, MD5),
// which is what monerod uses when started with --rpc-login.
//
// The most recent challenge is cached so that subsequent requests can
// authenticate up front instead of paying for a 401 round trip each time.
type digestTransport struct {
	user, pass string
	next       http.RoundTripper

	mu    sync.Mutex
	chal  *challenge
	count uint32
}

type challenge struct {
	realm, nonce, opaque, algorithm, qop string
}

func newDigestTransport(user, pass string, next http.RoundTripper) *digestTransport {
	if next == nil {
		next = http.DefaultTransport
	}
	return &digestTransport{user: user, pass: pass, next: next}
}

func (t *digestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if auth := t.authorize(req); auth != "" {
		r, err := cloneRequest(req)
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", auth)
		resp, err := t.next.RoundTrip(r)
		if err != nil || resp.StatusCode != http.StatusUnauthorized {
			return resp, err
		}
		// Cached challenge went stale; fall through with the fresh one.
		return t.retry(req, resp)
	}

	r, err := cloneRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := t.next.RoundTrip(r)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	return t.retry(req, resp)
}

// retry parses the challenge from a 401 response and re-sends the request
// with credentials. If the response carries no digest challenge it is
// returned unchanged.
func (t *digestTransport) retry(req *http.Request, resp *http.Response) (*http.Response, error) {
	c := parseChallenge(resp.Header.Get("WWW-Authenticate"))
	if c == nil {
		return resp, nil
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	t.mu.Lock()
	t.chal = c
	t.count = 0
	t.mu.Unlock()

	r, err := cloneRequest(req)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", t.authorize(req))
	return t.next.RoundTrip(r)
}

// authorize builds an Authorization header from the cached challenge, or
// returns "" when no challenge has been seen yet.
func (t *digestTransport) authorize(req *http.Request) string {
	t.mu.Lock()
	c := t.chal
	if c == nil {
		t.mu.Unlock()
		return ""
	}
	t.count++
	nc := fmt.Sprintf("%08x", t.count)
	t.mu.Unlock()

	uri := req.URL.RequestURI()
	ha1 := md5hex(t.user + ":" + c.realm + ":" + t.pass)
	cnonce := randomHex(16)
	if strings.EqualFold(c.algorithm, "MD5-sess") {
		ha1 = md5hex(ha1 + ":" + c.nonce + ":" + cnonce)
	}
	ha2 := md5hex(req.Method + ":" + uri)

	var response string
	qop := ""
	if hasToken(c.qop, "auth") {
		qop = "auth"
		response = md5hex(strings.Join([]string{ha1, c.nonce, nc, cnonce, qop, ha2}, ":"))
	} else {
		response = md5hex(ha1 + ":" + c.nonce + ":" + ha2)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `Digest username=%q, realm=%q, nonce=%q, uri=%q, response=%q`,
		t.user, c.realm, c.nonce, uri, response)
	if c.algorithm != "" {
		fmt.Fprintf(&b, ", algorithm=%s", c.algorithm)
	}
	if c.opaque != "" {
		fmt.Fprintf(&b, ", opaque=%q", c.opaque)
	}
	if qop != "" {
		fmt.Fprintf(&b, `, qop=%s, nc=%s, cnonce=%q`, qop, nc, cnonce)
	}
	return b.String()
}

// parseChallenge extracts the first MD5 digest challenge from a
// WWW-Authenticate header. monerod may send several (MD5 and MD5-sess)
// either comma-joined or as separate headers; http.Header.Get returns the
// first, which is sufficient.
func parseChallenge(h string) *challenge {
	const prefix = "digest "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return nil
	}
	params := parseParams(h[len(prefix):])
	c := &challenge{
		realm:     params["realm"],
		nonce:     params["nonce"],
		opaque:    params["opaque"],
		algorithm: params["algorithm"],
		qop:       params["qop"],
	}
	if c.nonce == "" {
		return nil
	}
	if a := strings.ToUpper(c.algorithm); a != "" && a != "MD5" && a != "MD5-SESS" {
		return nil
	}
	return c
}

// parseParams parses a comma-separated list of key=value or key="value"
// pairs. Parsing stops at a repeated key, which marks the start of a second
// challenge in a comma-joined header.
func parseParams(s string) map[string]string {
	out := map[string]string{}
	for {
		s = strings.TrimLeft(s, " \t,")
		if s == "" {
			return out
		}
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			return out
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		if sp := strings.LastIndexAny(key, " \t"); sp >= 0 {
			// "Digest realm=..." for a following challenge.
			return out
		}
		s = s[eq+1:]
		var val string
		if strings.HasPrefix(s, `"`) {
			var b strings.Builder
			i := 1
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				b.WriteByte(s[i])
			}
			val = b.String()
			if i < len(s) {
				i++
			}
			s = s[i:]
		} else {
			end := strings.IndexByte(s, ',')
			if end < 0 {
				end = len(s)
			}
			val = strings.TrimSpace(s[:end])
			s = s[end:]
		}
		if _, dup := out[key]; dup {
			return out
		}
		out[key] = val
	}
}

func hasToken(list, tok string) bool {
	for _, t := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(t), tok) {
			return true
		}
	}
	return false
}

func cloneRequest(req *http.Request) (*http.Request, error) {
	r := req.Clone(req.Context())
	if req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			return nil, fmt.Errorf("digest auth: request body cannot be replayed")
		}
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		r.Body = body
	}
	return r, nil
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
