package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
	"github.com/jonhyoliveira/monerod-dashboard/internal/xmr"
)

// ---- Tools ----

type toolsData struct {
	Op   string
	Form func(string) string
	Err  error

	Fee result[*rpc.FeeEstimate]

	KeyImages []keyImageStatus
	Outs      []rpc.Out
	Histogram []rpc.HistogramEntry
	Distrib   []distribSummary
	Coinbase  *rpc.CoinbaseTxSum
	TxIDs     []string
	PowHash   string
	BlockHash string
	SendRaw   *rpc.SendRawTxResult
	Payment   *paymentCheck
}

// paymentCheck is the result of proving a payment with a transaction key.
type paymentCheck struct {
	Proof   *xmr.Proof
	Address *xmr.Address
	Tx      rpc.Transaction
}

type keyImageStatus struct {
	KeyImage string
	Status   string
}

type distribSummary struct {
	Amount      uint64
	StartHeight uint64
	Base        uint64
	Blocks      int
	Total       uint64
	Rows        []distribRow
}

type distribRow struct {
	Height uint64
	Count  uint64
}

var hexRE = regexp.MustCompile(`^[0-9a-fA-F]*$`)

// fields splits a textarea of values separated by whitespace or commas.
func fields(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' })
}

func parseUints(s string) ([]uint64, error) {
	var out []uint64
	for _, f := range fields(s) {
		n, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", f)
		}
		out = append(out, n)
	}
	return out, nil
}

func (s *Server) pageTools(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	get := func(k string) string { return strings.TrimSpace(r.Form.Get(k)) }
	uintField := func(k string, def uint64) (uint64, error) {
		v := get(k)
		if v == "" {
			return def, nil
		}
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be a whole number", strings.ReplaceAll(k, "_", " "))
		}
		return n, nil
	}
	d := toolsData{Op: get("op"), Form: get, Fee: try(s.rpc.GetFeeEstimate(ctx))}

	switch d.Op {
	case "":
	case "key_images":
		imgs := fields(get("key_images"))
		for _, k := range imgs {
			if !rpc.IsHash(k) {
				d.Err = fmt.Errorf("%q is not a 64-character hex key image", k)
			}
		}
		if d.Err == nil && len(imgs) > 0 {
			var st []int
			if st, d.Err = s.rpc.IsKeyImageSpent(ctx, imgs); d.Err == nil {
				for i, k := range imgs {
					label := "unknown"
					if i < len(st) {
						label = map[int]string{rpc.KeyImageUnspent: "unspent", rpc.KeyImageSpentInChain: "spent (in blockchain)", rpc.KeyImageSpentInPool: "spent (in pool)"}[st[i]]
					}
					d.KeyImages = append(d.KeyImages, keyImageStatus{KeyImage: k, Status: label})
				}
			}
		}
	case "outs":
		var amount uint64
		var idx []uint64
		if amount, d.Err = uintField("amount", 0); d.Err == nil {
			if idx, d.Err = parseUints(get("indices")); d.Err == nil {
				refs := make([]rpc.OutRef, 0, len(idx))
				for _, i := range idx {
					refs = append(refs, rpc.OutRef{Amount: amount, Index: i})
				}
				if len(refs) > 1000 {
					d.Err = errors.New("look up at most 1000 outputs at a time")
				} else {
					d.Outs, d.Err = s.rpc.GetOuts(ctx, refs)
				}
			}
		}
	case "histogram":
		p := rpc.HistogramParams{Unlocked: get("unlocked") != ""}
		if p.Amounts, d.Err = parseUints(get("amounts")); d.Err == nil {
			if p.MinCount, d.Err = uintField("min_count", 0); d.Err == nil {
				if p.MaxCount, d.Err = uintField("max_count", 0); d.Err == nil {
					if p.RecentCutoff, d.Err = uintField("recent_cutoff", 0); d.Err == nil {
						d.Histogram, d.Err = s.rpc.GetOutputHistogram(ctx, p)
					}
				}
			}
		}
	case "distribution":
		var amounts []uint64
		var from, to uint64
		if amounts, d.Err = parseUints(get("amounts")); d.Err == nil {
			if from, d.Err = uintField("from_height", 0); d.Err == nil {
				if to, d.Err = uintField("to_height", 0); d.Err == nil {
					var dists []rpc.Distribution
					if dists, d.Err = s.rpc.GetOutputDistribution(ctx, amounts, from, to, get("cumulative") != ""); d.Err == nil {
						for _, di := range dists {
							sum := distribSummary{Amount: di.Amount, StartHeight: di.StartHeight, Base: di.Base, Blocks: len(di.Distribution)}
							for i, c := range di.Distribution {
								sum.Total += c
								// Show the last 50 blocks; the total covers all.
								if i >= len(di.Distribution)-50 {
									sum.Rows = append(sum.Rows, distribRow{Height: di.StartHeight + uint64(i), Count: c})
								}
							}
							d.Distrib = append(d.Distrib, sum)
						}
					}
				}
			}
		}
	case "coinbase_sum":
		var h, n uint64
		if h, d.Err = uintField("height", 0); d.Err == nil {
			if n, d.Err = uintField("count", 1000); d.Err == nil {
				d.Coinbase, d.Err = s.rpc.GetCoinbaseTxSum(ctx, h, n)
			}
		}
	case "txids_loose":
		tpl := get("txid_template")
		var bits uint64
		if bits, d.Err = uintField("num_matching_bits", 0); d.Err == nil {
			if !rpc.IsHash(tpl) {
				d.Err = errors.New("txid template must be 64 hex characters")
			} else {
				d.TxIDs, d.Err = s.rpc.GetTxidsLoose(ctx, tpl, uint32(min(bits, 256)))
			}
		}
	case "block_hash":
		var h uint64
		if h, d.Err = uintField("height", 0); d.Err == nil {
			d.BlockHash, d.Err = s.rpc.GetBlockHash(ctx, h)
		}
	case "calc_pow":
		p := rpc.CalcPowParams{BlockBlob: get("block_blob"), SeedHash: get("seed_hash")}
		var major, height uint64
		if major, d.Err = uintField("major_version", 16); d.Err == nil {
			if height, d.Err = uintField("height", 0); d.Err == nil {
				p.MajorVersion, p.Height = uint8(min(major, 255)), height
				switch {
				case p.BlockBlob == "" || !hexRE.MatchString(p.BlockBlob):
					d.Err = errors.New("block blob must be hex")
				case !rpc.IsHash(p.SeedHash):
					d.Err = errors.New("seed hash must be 64 hex characters")
				default:
					d.PowHash, d.Err = s.rpc.CalcPow(ctx, p)
				}
			}
		}
	case "send_raw_tx":
		if r.Method != http.MethodPost {
			d.Err = errors.New("broadcasting needs a POST")
			break
		}
		txHex := get("tx_as_hex")
		if txHex == "" || !hexRE.MatchString(txHex) || len(txHex)%2 != 0 {
			d.Err = errors.New("transaction must be hex")
			break
		}
		d.SendRaw, d.Err = s.rpc.SendRawTransaction(ctx, txHex, get("do_not_relay") != "", get("do_sanity_checks") != "")
		s.rpc.Invalidate()
		log.Printf("action=send_raw_transaction ip=%s bytes=%d err=%v", clientIP(r), len(txHex)/2, d.Err)
	case "prove_payment":
		// POST only: the transaction key shouldn't end up in URLs and history.
		if r.Method != http.MethodPost {
			d.Err = errors.New("checking a payment needs a POST")
			break
		}
		d.Payment, d.Err = s.provePayment(r.Context(), get("txid"), get("tx_key"), get("address"))
	default:
		d.Err = fmt.Errorf("unknown tool %q", d.Op)
	}
	s.render(w, r, "tools", view{Title: "Tools", Data: d})
}

// ---- RPC console ----

// consoleMethods lists the JSON-RPC methods offered in the console's
// suggestions; any other name can be typed.
var consoleMethods = []string{
	"get_info", "get_version", "get_block_count", "on_get_block_hash", "get_block_template", "get_miner_data",
	"calc_pow", "add_aux_pow", "submit_block", "generateblocks", "get_last_block_header",
	"get_block_header_by_hash", "get_block_header_by_height", "get_block_headers_range", "get_block",
	"get_connections", "hard_fork_info", "set_bans", "get_bans", "banned", "flush_txpool",
	"get_output_histogram", "get_coinbase_tx_sum", "get_fee_estimate", "get_alternate_chains", "relay_tx",
	"sync_info", "get_txpool_backlog", "get_output_distribution", "prune_blockchain", "flush_cache",
	"get_txids_loose",
}

var consolePaths = []string{
	"/get_height", "/get_transactions", "/get_alt_blocks_hashes", "/is_key_image_spent", "/send_raw_transaction",
	"/start_mining", "/stop_mining", "/mining_status", "/save_bc", "/get_peer_list", "/get_public_nodes",
	"/set_log_hash_rate", "/set_log_level", "/set_log_categories", "/get_transaction_pool",
	"/get_transaction_pool_hashes", "/get_transaction_pool_stats", "/stop_daemon", "/get_info", "/get_net_stats",
	"/get_limit", "/set_limit", "/out_peers", "/in_peers", "/get_outs", "/update", "/pop_blocks",
}

var pathRE = regexp.MustCompile(`^/[a-z_]+$`)

type consoleData struct {
	Methods  []string
	Paths    []string
	Endpoint string
	Method   string
	Params   string
	Response string
	Err      error
	Ran      bool
}

func (s *Server) pageConsole(w http.ResponseWriter, r *http.Request) {
	d := consoleData{Methods: consoleMethods, Paths: consolePaths, Endpoint: "/json_rpc", Params: "{}"}
	if r.Method == http.MethodPost {
		d.Ran = true
		d.Endpoint = strings.TrimSpace(r.PostFormValue("endpoint"))
		d.Method = strings.TrimSpace(r.PostFormValue("method"))
		d.Params = strings.TrimSpace(r.PostFormValue("params"))
		if d.Params == "" {
			d.Params = "{}"
		}
		switch {
		case !pathRE.MatchString(d.Endpoint):
			d.Err = errors.New("endpoint must be /json_rpc or a JSON endpoint such as /get_transactions (binary .bin endpoints are not supported)")
		case d.Endpoint == "/json_rpc" && d.Method == "":
			d.Err = errors.New("enter a JSON-RPC method name")
		case !json.Valid([]byte(d.Params)):
			d.Err = errors.New("params must be valid JSON")
		default:
			var raw []byte
			raw, d.Err = s.rpc.Raw(r.Context(), d.Endpoint, d.Method, json.RawMessage(d.Params))
			// The request may have changed anything.
			s.rpc.Invalidate()
			d.Response = prettyRaw(raw)
		}
		log.Printf("action=console ip=%s endpoint=%s method=%s err=%v", clientIP(r), d.Endpoint, d.Method, d.Err)
	}
	s.render(w, r, "console", view{Title: "RPC console", Data: d})
}

func prettyRaw(raw []byte) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return strings.ToValidUTF8(string(raw), "�")
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	return string(out)
}

// provePayment checks which outputs of txid pay address, given the
// sender's transaction key: what monero-wallet-rpc's check_tx_key does,
// with the transaction fetched from monerod.
func (s *Server) provePayment(ctx context.Context, txid, txKey, address string) (*paymentCheck, error) {
	if !rpc.IsHash(txid) {
		return nil, errors.New("transaction ID must be 64 hex characters")
	}
	addr, err := xmr.ParseAddress(address)
	if err != nil {
		return nil, err
	}
	if _, _, err := xmr.ParseTxKey(txKey); err != nil {
		return nil, err
	}
	// Regtest ("fakechain") uses mainnet addresses.
	if info, err := s.rpc.GetInfo(ctx); err == nil && info.Nettype != addr.Network &&
		!(info.Nettype == "fakechain" && addr.Network == "mainnet") {
		return nil, fmt.Errorf("that is a %s address, but this node is on %s", addr.Network, info.Nettype)
	}
	txs, missed, err := s.rpc.GetTransactions(ctx, []string{txid})
	if err != nil {
		return nil, err
	}
	if len(missed) > 0 || len(txs) == 0 {
		return nil, errors.New("monerod doesn't know this transaction (not in its blockchain or pool)")
	}
	proof, err := xmr.CheckTxKey(txs[0].AsJSON, txKey, addr)
	if err != nil {
		return nil, err
	}
	return &paymentCheck{Proof: proof, Address: addr, Tx: txs[0]}, nil
}
