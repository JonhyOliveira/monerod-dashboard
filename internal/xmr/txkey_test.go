package xmr

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/check_tx_key.json holds regtest transactions with the answers
// monero-wallet-rpc's check_tx_key gave for them: standard, subaddress and
// integrated recipients, change outputs, and a transaction with per-output
// keys.
func TestCheckTxKeyMatchesWallet(t *testing.T) {
	for _, c := range loadCases(t) {
		addr, err := ParseAddress(c.Address)
		if err != nil {
			t.Fatalf("%s: %v", c.Address, err)
		}
		p, err := CheckTxKey(c.AsJSON, c.TxKey, addr)
		if err != nil {
			t.Fatal(err)
		}
		want := c.Received
		name := c.TxID[:8] + " " + addr.Kind
		if p.Total != want {
			t.Errorf("%s: received %d, wallet says %d", name, p.Total, want)
		}
		// A key can only be tied to the transaction from an address it paid:
		// a payment to one subaddress derives the transaction's public key
		// from that subaddress.
		if want > 0 && !p.KeyMatches {
			t.Errorf("%s: key does not match the transaction", name)
		}
		if addr.Kind == "integrated" && p.PaymentID != "01a628e7c5e4df74" {
			t.Errorf("%s: payment ID %q", name, p.PaymentID)
		}
		t.Logf("%s: %d outputs, %d", name, len(p.Outputs), p.Total)
	}
}

type fixture struct {
	TxID     string `json:"txid"`
	TxKey    string `json:"tx_key"`
	Address  string `json:"address"`
	AsJSON   string `json:"as_json"`
	Received uint64 `json:"received"`
}

func loadCases(t *testing.T) []fixture {
	raw, err := os.ReadFile("testdata/check_tx_key.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []fixture
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func TestCheckTxKeyWrongKey(t *testing.T) {
	cases := loadCases(t)
	addr, _ := ParseAddress(cases[0].Address)
	p, err := CheckTxKey(cases[0].AsJSON, cases[2].TxKey, addr)
	if err != nil {
		t.Fatal(err)
	}
	if p.KeyMatches || len(p.Outputs) != 0 {
		t.Errorf("another transaction's key: matches=%v outputs=%d", p.KeyMatches, len(p.Outputs))
	}
}

func TestParseAddress(t *testing.T) {
	// The Monero general fund's published address.
	a, err := ParseAddress("44AFFq5kSiGBoZ4NMDwYtN18obc8AemS33DBLWs3H7otXft3XjrpDtQGv7SqSsaBYBb98uNbr2VBBEt7f2wfn3RVGQBEP3A")
	if err != nil || a.Network != "mainnet" || a.Kind != "standard" {
		t.Fatalf("general fund address: %+v, %v", a, err)
	}
	for _, bad := range []string{
		"44AFFq5kSiGBoZ4NMDwYtN18obc8AemS33DBLWs3H7otXft3XjrpDtQGv7SqSsaBYBb98uNbr2VBBEt7f2wfn3RVGQBEP3B", // checksum
		"44AFFq5kSiGBoZ4NMDwYtN18obc8AemS33DBLWs3H7otXft3XjrpDtQGv7SqSsaBYBb98uNbr2VBBEt7f2wfn3RVGQBEP",   // length
		"0OIl", "",
	} {
		if _, err := ParseAddress(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestParseTxKey(t *testing.T) {
	for _, bad := range []string{"", "abc", "zz" + string(make([]byte, 62)),
		// l itself is not a reduced scalar.
		"edd3f55c1a631258d69cf7a2def9de1400000000000000000000000000000010"} {
		if _, _, err := ParseTxKey(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
