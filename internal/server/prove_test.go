package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestProvePayment(t *testing.T) {
	raw, err := os.ReadFile("../xmr/testdata/check_tx_key.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		TxID    string `json:"txid"`
		TxKey   string `json:"tx_key"`
		Address string `json:"address"`
		AsJSON  string `json:"as_json"`
	}
	json.Unmarshal(raw, &cases)
	c := cases[6] // 0.1 XMR to a subaddress, in a transaction with per-output keys

	e := newEnv(t)
	e.daemon.Handle("get_transactions", func(json.RawMessage) any {
		return map[string]any{"status": "OK", "txs": []map[string]any{{
			"tx_hash": c.TxID, "as_json": c.AsJSON, "block_height": 140, "confirmations": 3,
		}}}
	})
	e.login()
	form := url.Values{"csrf": {e.csrf}, "op": {"prove_payment"}, "txid": {c.TxID}, "tx_key": {c.TxKey}, "address": {c.Address}}
	resp, body := e.post("/tools", form)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	mustContain(t, body, "pays this subaddress <strong>0.1", "Confirmed in block", "(3 confirmations)")

	// Wrong address: nothing paid.
	form.Set("address", "44AFFq5kSiGBoZ4NMDwYtN18obc8AemS33DBLWs3H7otXft3XjrpDtQGv7SqSsaBYBb98uNbr2VBBEt7f2wfn3RVGQBEP3A")
	_, body = e.post("/tools", form)
	mustContain(t, body, "Nothing in this transaction pays this address.")

	// Bad input and GET are refused before touching the daemon.
	form.Set("address", "not-an-address")
	_, body = e.post("/tools", form)
	mustContain(t, body, "not a valid address")
	_, body = e.get("/tools?op=prove_payment&txid=" + c.TxID + "&tx_key=" + c.TxKey + "&address=" + url.QueryEscape(c.Address))
	mustContain(t, body, "needs a POST")
	if strings.Contains(body, "pays this") {
		t.Error("GET ran the check")
	}
	if n := len(e.daemon.Calls("get_transactions")); n != 2 {
		t.Errorf("get_transactions called %d times, want 2", n)
	}
}
