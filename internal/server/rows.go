package server

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
)

// Long tables (mempool transactions, peer lists) render their first rowsPerPage
// rows with the page. The last row is a sentinel that htmx swaps for the next
// batch once it scrolls into view (a "Next" link without JavaScript).
// Live refreshes pause while a batch loads or extra batches are shown.
//
// monerod can't page these lists itself, so every batch is cut from the same
// cached snapshot. Batches continue after a cursor (the last row's sort key)
// rather than an offset, so rows arriving or leaving between batches don't
// cause duplicates or gaps.
const rowsPerPage = 100

// rowsPage is one batch of a long table.
type rowsPage[T any] struct {
	Rows      []T
	Total     int    // rows in the whole list
	Next      string // cursor for the next batch; empty on the last one
	Remaining int    // rows after this batch
	Extra     bool   // loaded after the page (marks rows that pause live refresh)
	MoreURL   string // htmx endpoint for the next batch, without the cursor
	PageURL   string // full-page fallback for the next batch, without the cursor
	List      string // which list, for tables that share a template
}

// paginate returns the batch of sorted that follows cursor.
func paginate[T any](sorted []T, cursor string, after func(T, string) bool, key func(T) string) rowsPage[T] {
	start := 0
	if cursor != "" {
		start = len(sorted)
		for i, x := range sorted {
			if after(x, cursor) {
				start = i
				break
			}
		}
	}
	end := min(start+rowsPerPage, len(sorted))
	p := rowsPage[T]{Rows: sorted[start:end], Total: len(sorted), Remaining: len(sorted) - end}
	if end < len(sorted) {
		p.Next = key(sorted[end-1])
	}
	return p
}

// splitCursor parses "number:rest".
func splitCursor(c string) (int64, string) {
	n, rest, _ := strings.Cut(c, ":")
	v, _ := strconv.ParseInt(n, 10, 64)
	return v, rest
}

// ---- Mempool: newest first ----

func sortedPool(txs []rpc.PoolTx) []rpc.PoolTx {
	out := slices.Clone(txs) // cached results are shared: sort a copy
	slices.SortStableFunc(out, func(a, b rpc.PoolTx) int {
		if a.ReceiveTime != b.ReceiveTime {
			return int(b.ReceiveTime - a.ReceiveTime)
		}
		return strings.Compare(a.IDHash, b.IDHash)
	})
	return out
}

func poolPage(txs []rpc.PoolTx, cursor string) rowsPage[rpc.PoolTx] {
	p := paginate(sortedPool(txs), cursor,
		func(tx rpc.PoolTx, c string) bool {
			t, h := splitCursor(c)
			return tx.ReceiveTime < t || (tx.ReceiveTime == t && tx.IDHash > h)
		},
		func(tx rpc.PoolTx) string { return fmt.Sprintf("%d:%s", tx.ReceiveTime, tx.IDHash) })
	p.MoreURL, p.PageURL = "/mempool/rows?after=", "/mempool?after="
	return p
}

// ---- Peer lists: most recently seen first ----

func peerAddr(p rpc.Peer) string { return fmt.Sprintf("%s:%d", p.Host, p.Port) }

func sortedPeers(peers []rpc.Peer) []rpc.Peer {
	out := slices.Clone(peers)
	slices.SortStableFunc(out, func(a, b rpc.Peer) int {
		if a.LastSeen != b.LastSeen {
			return int(b.LastSeen - a.LastSeen)
		}
		return strings.Compare(peerAddr(a), peerAddr(b))
	})
	return out
}

func peerPage(list string, peers []rpc.Peer, cursor string) rowsPage[rpc.Peer] {
	p := paginate(sortedPeers(peers), cursor,
		func(pe rpc.Peer, c string) bool {
			t, addr := splitCursor(c)
			return pe.LastSeen < t || (pe.LastSeen == t && peerAddr(pe) > addr)
		},
		func(pe rpc.Peer) string { return fmt.Sprintf("%d:%s", pe.LastSeen, peerAddr(pe)) })
	p.List = list
	p.MoreURL = "/peers/rows?list=" + list + "&after="
	p.PageURL = "/peers?" + list + "_after="
	return p
}

// ---- Next-batch endpoints (htmx) ----

func (s *Server) mempoolRows(w http.ResponseWriter, r *http.Request) {
	after := r.URL.Query().Get("after")
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, "/mempool?after="+url.QueryEscape(after), http.StatusSeeOther)
		return
	}
	pool, err := s.rpc.GetTransactionPool(r.Context())
	if err != nil {
		http.Error(w, errMessage(err), http.StatusBadGateway)
		return
	}
	p := poolPage(pool.Transactions, after)
	p.Extra = true
	s.renderFragment(w, "mempool", "pool-rows", p)
}

func (s *Server) peerRows(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, after := q.Get("list"), q.Get("after")
	if list != "white" && list != "gray" {
		http.Error(w, "unknown list", http.StatusBadRequest)
		return
	}
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, "/peers?"+list+"_after="+url.QueryEscape(after), http.StatusSeeOther)
		return
	}
	pl, err := s.rpc.GetPeerList(r.Context())
	if err != nil {
		http.Error(w, errMessage(err), http.StatusBadGateway)
		return
	}
	peers := pl.WhiteList
	if list == "gray" {
		peers = pl.GrayList
	}
	p := peerPage(list, peers, after)
	p.Extra = true
	s.renderFragment(w, "peers", "peer-rows", p)
}

// ---- Bans: by address, filtered by a search ----

// banKey orders hosts by address, then subnet size; anything that doesn't
// parse sorts last, by text.
func banKey(host string) (netip.Prefix, bool) {
	if p, err := netip.ParsePrefix(host); err == nil {
		return p.Masked(), true
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return netip.PrefixFrom(a, a.BitLen()), true
	}
	return netip.Prefix{}, false
}

func compareHosts(a, b string) int {
	pa, oka := banKey(a)
	pb, okb := banKey(b)
	switch {
	case oka && okb:
		if c := pa.Addr().Compare(pb.Addr()); c != 0 {
			return c
		}
		if c := pa.Bits() - pb.Bits(); c != 0 {
			return c
		}
	case oka:
		return -1
	case okb:
		return 1
	}
	return strings.Compare(a, b)
}

// banMatches reports whether a ban matches the search q: its text contains
// q, or q is an address inside the banned subnet.
func banMatches(b rpc.Ban, q string) bool {
	if strings.Contains(strings.ToLower(b.Host), strings.ToLower(q)) {
		return true
	}
	a, err := netip.ParseAddr(q)
	if err != nil {
		return false
	}
	p, ok := banKey(b.Host)
	return ok && p.Contains(a)
}

// banRows is a batch of the ban table. The rows carry unban forms, so it
// needs the page's CSRF token and path.
type banRows struct {
	Page       rowsPage[rpc.Ban]
	Query      string
	Unfiltered int // bans before the search
	CSRF, Path string
}

func banPage(bans []rpc.Ban, q, cursor string) banRows {
	q = strings.TrimSpace(q)
	var out []rpc.Ban
	for _, b := range bans {
		if q == "" || banMatches(b, q) {
			out = append(out, b)
		}
	}
	slices.SortStableFunc(out, func(a, b rpc.Ban) int { return compareHosts(a.Host, b.Host) })
	p := paginate(out, cursor,
		func(b rpc.Ban, c string) bool { return compareHosts(b.Host, c) > 0 },
		func(b rpc.Ban) string { return b.Host })
	qs := "ban_q=" + url.QueryEscape(q)
	p.MoreURL = "/peers/bans?" + qs + "&after="
	p.PageURL = "/peers?" + qs + "&ban_after="
	return banRows{Page: p, Query: q, Unfiltered: len(bans)}
}

// banURL is the Peers page showing search q.
func banURL(q string) string {
	if q == "" {
		return "/peers"
	}
	return "/peers?ban_q=" + url.QueryEscape(q)
}

// banRowsHandler serves a search of the ban table (the whole table) or its
// next batch (rows only), and keeps the search in the address bar so live
// refreshes and reloads keep it.
func (s *Server) banRowsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query, after := strings.TrimSpace(q.Get("ban_q")), q.Get("after")
	if r.Header.Get("HX-Request") != "true" {
		u := banURL(query)
		if after != "" {
			u = "/peers?ban_q=" + url.QueryEscape(query) + "&ban_after=" + url.QueryEscape(after)
		}
		http.Redirect(w, r, u, http.StatusSeeOther)
		return
	}
	bans, err := s.rpc.GetBans(r.Context())
	if err != nil {
		http.Error(w, errMessage(err), http.StatusBadGateway)
		return
	}
	p := banPage(bans, query, after)
	if sess := sessionFrom(r); sess != nil {
		p.CSRF = sess.csrf
	}
	p.Path = banURL(query)
	if after != "" {
		p.Page.Extra = true
		s.renderFragment(w, "peers", "ban-rows", p)
		return
	}
	w.Header().Set("HX-Replace-Url", p.Path)
	s.renderFragment(w, "peers", "ban-results", p)
}
