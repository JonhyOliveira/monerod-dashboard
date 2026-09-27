package server

import (
	"fmt"
	"net/http"
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
