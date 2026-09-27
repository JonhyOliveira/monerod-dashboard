package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
)

func poolTxs(n int, newest int64) []rpc.PoolTx {
	txs := make([]rpc.PoolTx, n)
	for i := range txs {
		txs[i] = rpc.PoolTx{IDHash: fmt.Sprintf("%064x", i), ReceiveTime: newest - int64(i/3), Fee: 1, Weight: 1} // ties on time
	}
	return txs
}

func TestPoolPageWalksWholeList(t *testing.T) {
	txs := poolTxs(250, 1000)
	seen := map[string]bool{}
	cursor, batches := "", 0
	for {
		p := poolPage(txs, cursor)
		batches++
		for _, tx := range p.Rows {
			if seen[tx.IDHash] {
				t.Fatalf("duplicate row %s", tx.IDHash)
			}
			seen[tx.IDHash] = true
		}
		if p.Next == "" {
			if p.Remaining != 0 {
				t.Fatalf("last batch reports %d remaining", p.Remaining)
			}
			break
		}
		cursor = p.Next
	}
	if len(seen) != 250 || batches != 3 {
		t.Fatalf("saw %d rows in %d batches", len(seen), batches)
	}
}

// Rows arriving or leaving between batches neither repeat nor skip rows.
func TestPoolPageCursorIsStable(t *testing.T) {
	txs := poolTxs(250, 1000)
	first := poolPage(txs, "")
	lastShown := first.Rows[len(first.Rows)-1]

	// New transactions arrive at the top; some shown ones are mined.
	changed := append(poolTxs(0, 0), rpc.PoolTx{IDHash: strings.Repeat("f", 64), ReceiveTime: 2000})
	for _, tx := range txs {
		if tx.IDHash != first.Rows[5].IDHash && tx.IDHash != lastShown.IDHash {
			changed = append(changed, tx)
		}
	}
	second := poolPage(changed, first.Next)
	if len(second.Rows) == 0 {
		t.Fatal("empty second batch")
	}
	sorted := sortedPool(txs)
	if second.Rows[0].IDHash != sorted[100].IDHash {
		t.Fatalf("second batch starts at %s, want %s (the row after the last one shown)", second.Rows[0].IDHash, sorted[100].IDHash)
	}
}

var sentinelRE = regexp.MustCompile(`hx-get="(/mempool/rows\?after=[^"]+)"`)

func TestMempoolRowsEndpoint(t *testing.T) {
	e := newEnv(t)
	txs := poolTxs(250, 1790495210)
	e.daemon.Handle("get_transaction_pool", func(json.RawMessage) any {
		return map[string]any{"status": "OK", "transactions": txs}
	})
	e.login()

	_, body := e.get("/mempool")
	if n := strings.Count(body, `name="txid"`); n != 100 {
		t.Fatalf("page renders %d rows, want 100", n)
	}
	// hx-target must be explicit: the rows sit inside an action form whose
	// inherited target is #toasts.
	mustContain(t, body, "Next 100 of 150 more", `hx-trigger="intersect once" hx-target="this"`)

	next := sentinelRE.FindStringSubmatch(body)
	if next == nil {
		t.Fatal("no sentinel row")
	}
	path := strings.ReplaceAll(next[1], "&amp;", "&")
	resp, rows := e.get(path, "HX-Request", "true")
	if resp.StatusCode != http.StatusOK || strings.Contains(rows, "<html") {
		t.Fatalf("rows: %d", resp.StatusCode)
	}
	if n := strings.Count(rows, `name="txid"`); n != 100 {
		t.Fatalf("second batch has %d rows", n)
	}
	mustContain(t, rows, "data-extra", "Next 50 of 50 more")

	path = strings.ReplaceAll(sentinelRE.FindStringSubmatch(rows)[1], "&amp;", "&")
	_, rows = e.get(path, "HX-Request", "true")
	if n := strings.Count(rows, `name="txid"`); n != 50 || strings.Contains(rows, "more-rows") {
		t.Fatalf("last batch: %d rows, sentinel present: %v", n, strings.Contains(rows, "more-rows"))
	}

	// Without JavaScript the link opens the batch as a full, non-live page.
	resp, _ = e.get(path)
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/mempool?after=") {
		t.Fatalf("no-JS: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, page := e.get(resp.Header.Get("Location"))
	if strings.Contains(page, `id="refresh"`) || strings.Count(page, `name="txid"`) != 50 {
		t.Fatal("no-JS batch page should show the batch without live refresh")
	}
}

func TestPeerRowsEndpoint(t *testing.T) {
	e := newEnv(t)
	gray := make([]rpc.Peer, 130)
	for i := range gray {
		gray[i] = rpc.Peer{Host: fmt.Sprintf("10.0.%d.%d", i/250, i%250), Port: 18080, LastSeen: int64(5000 - i)}
	}
	e.daemon.Handle("get_peer_list", func(json.RawMessage) any {
		return map[string]any{"status": "OK", "gray_list": gray}
	})
	e.login()
	_, body := e.get("/peers")
	mustContain(t, body, "Gray list: 130 peers", `/peers/rows?list=gray&amp;after=`, "Next 30 of 30 more")
	resp, rows := e.get("/peers/rows?list=gray&after=4901:10.0.0.99:18080", "HX-Request", "true")
	if resp.StatusCode != http.StatusOK || strings.Count(rows, "<tr>") != 30 {
		t.Fatalf("rows: %d, %d rows", resp.StatusCode, strings.Count(rows, "<tr>"))
	}
	if resp, _ := e.get("/peers/rows?list=bogus", "HX-Request", "true"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad list: %d", resp.StatusCode)
	}
}
