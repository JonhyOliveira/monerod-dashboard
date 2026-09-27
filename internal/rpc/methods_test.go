package rpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc/rpctest"
)

// The fixtures were recorded from a regtest monerod 0.18.3.1 at height 131
// with two pool transactions, one peer (both directions) and two bans.

func client(t *testing.T) (*rpc.Client, *rpctest.Daemon) {
	d := rpctest.New(t)
	return rpc.New(rpc.Options{URL: d.URL}), d
}

// must fails the test (via panic, which the test runner reports with the
// error) when a call returns an error.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadMethodsDecodeFixtures(t *testing.T) {
	c, _ := client(t)
	ctx := context.Background()

	info := must(c.GetInfo(ctx))
	if info.Height != 131 || info.Nettype != "fakechain" || info.TxPoolSize != 2 {
		t.Errorf("get_info: %+v", info)
	}
	if v := must(c.GetVersion(ctx)); v.Version>>16 != 3 || len(v.HardForks) != 2 {
		t.Errorf("get_version: %+v", v)
	}
	if hf := must(c.HardForkInfo(ctx)); hf.Version != 16 || !hf.Enabled {
		t.Errorf("hard_fork_info: %+v", hf)
	}
	if si := must(c.SyncInfo(ctx)); si.Height != 131 || len(si.Peers) != 2 {
		t.Errorf("sync_info: %+v", si)
	}
	if ns := must(c.GetNetStats(ctx)); ns.TotalBytesOut == 0 {
		t.Errorf("get_net_stats: %+v", ns)
	}
	if l := must(c.GetLimit(ctx)); l.LimitDown != 8192 || l.LimitUp != 2048 {
		t.Errorf("get_limit: %+v", l)
	}
	if f := must(c.GetFeeEstimate(ctx)); f.Fee == 0 || len(f.Fees) != 4 {
		t.Errorf("get_fee_estimate: %+v", f)
	}

	if h := must(c.GetLastBlockHeader(ctx)); h.Height != 130 || !rpc.IsHash(h.Hash) {
		t.Errorf("get_last_block_header: %+v", h)
	}
	if hs := must(c.GetBlockHeadersRange(ctx, 120, 130)); len(hs) != 11 || hs[0].Height != 120 {
		t.Errorf("get_block_headers_range: %d headers", len(hs))
	}
	if b := must(c.GetBlockByHeight(ctx, 100)); b.BlockHeader.Height != 100 || !strings.Contains(b.JSON, "miner_tx") {
		t.Errorf("get_block: %+v", b.BlockHeader)
	}
	if n := must(c.GetBlockCount(ctx)); n != 131 {
		t.Errorf("get_block_count = %d", n)
	}
	if h := must(c.GetBlockHash(ctx, 10)); !rpc.IsHash(h) {
		t.Errorf("on_get_block_hash = %q", h)
	}
	if s := must(c.GetCoinbaseTxSum(ctx, 0, 131)); s.EmissionAmount == 0 {
		t.Errorf("get_coinbase_tx_sum: %+v", s)
	}
	must(c.GetAlternateChains(ctx))
	must(c.GetAltBlocksHashes(ctx))

	if conns := must(c.GetConnections(ctx)); len(conns) != 2 || conns[0].Host != "127.0.0.1" {
		t.Errorf("get_connections: %+v", conns)
	}
	if bans := must(c.GetBans(ctx)); len(bans) != 2 || bans[1].Host != "192.168.50.0/24" {
		t.Errorf("get_bans: %+v", bans)
	}
	if banned, secs, err := c.Banned(ctx, "10.9.8.7"); err != nil || !banned || secs != 3600 {
		t.Errorf("banned: %v %d %v", banned, secs, err)
	}
	must(c.GetPeerList(ctx))
	must(c.GetPublicNodes(ctx))

	pool := must(c.GetTransactionPool(ctx))
	if len(pool.Transactions) != 2 || pool.Transactions[0].Fee == 0 || len(pool.SpentKeyImages) == 0 {
		t.Errorf("get_transaction_pool: %+v", pool)
	}
	if hs := must(c.GetTransactionPoolHashes(ctx)); len(hs) != 2 {
		t.Errorf("get_transaction_pool_hashes: %v", hs)
	}
	if st := must(c.GetTransactionPoolStats(ctx)); st.TxsTotal != 2 || st.BytesTotal != 4332 {
		t.Errorf("get_transaction_pool_stats: %+v", st)
	}

	txs, missed, err := c.GetTransactions(ctx, []string{pool.Transactions[0].IDHash})
	if err != nil || len(txs) != 2 || len(missed) != 0 || !strings.Contains(txs[0].AsJSON, "vin") {
		t.Errorf("get_transactions: %d txs, missed %v, err %v", len(txs), missed, err)
	}
	if st := must(c.IsKeyImageSpent(ctx, []string{strings.Repeat("00", 32)})); len(st) != 1 {
		t.Errorf("is_key_image_spent: %v", st)
	}
	if outs := must(c.GetOuts(ctx, []rpc.OutRef{{Amount: 0, Index: 1}})); len(outs) != 1 || !rpc.IsHash(outs[0].TxID) {
		t.Errorf("get_outs: %+v", outs)
	}
	if h := must(c.GetOutputHistogram(ctx, rpc.HistogramParams{Amounts: []uint64{0}})); len(h) != 1 || h[0].TotalInstances == 0 {
		t.Errorf("get_output_histogram: %+v", h)
	}
	if d := must(c.GetOutputDistribution(ctx, nil, 120, 130, false)); len(d) != 1 || len(d[0].Distribution) != 11 {
		t.Errorf("get_output_distribution: %+v", d)
	}

	if ms := must(c.GetMiningStatus(ctx)); ms.PowAlgorithm != "RandomX" {
		t.Errorf("mining_status: %+v", ms)
	}
	if md := must(c.GetMinerData(ctx)); md.Height != 131 || len(md.TxBacklog) != 2 {
		t.Errorf("get_miner_data: %+v", md)
	}
	if bt := must(c.GetBlockTemplate(ctx, "addr", 60)); bt.Height != 131 || bt.BlocktemplateBlob == "" {
		t.Errorf("get_block_template: %+v", bt)
	}
	if pr := must(c.PruneBlockchain(ctx, true)); pr.Pruned {
		t.Errorf("prune_blockchain check: %+v", pr)
	}
}

func TestTxpoolBacklogBinary(t *testing.T) {
	c, _ := client(t)
	backlog := must(c.GetTxpoolBacklog(context.Background()))
	if len(backlog) != 2 || backlog[0].Weight != 2166 || backlog[0].Fee != 2605200000 {
		t.Fatalf("backlog = %+v", backlog)
	}
}

func TestUpdateStatusError(t *testing.T) {
	c, _ := client(t)
	_, err := c.Update(context.Background(), "check")
	var se *rpc.StatusError
	if !errors.As(err, &se) || se.Status != "Error checking for updates" {
		t.Fatalf("err = %v", err)
	}
}

func TestUnsupportedMethod(t *testing.T) {
	c, _ := client(t)
	if _, err := c.GetTxidsLoose(context.Background(), "00", 8); !errors.Is(err, rpc.ErrUnsupported) {
		t.Fatalf("json-rpc: err = %v", err)
	}
	if err := c.CallPath(context.Background(), "/no_such_endpoint", nil, nil); !errors.Is(err, rpc.ErrUnsupported) {
		t.Fatalf("path: err = %v", err)
	}
}

// Actions send the parameters monerod expects.
func TestActionParams(t *testing.T) {
	c, d := client(t)
	ctx := context.Background()
	check := func(method, want string) {
		t.Helper()
		calls := d.Calls(method)
		if len(calls) == 0 {
			t.Fatalf("%s not called", method)
		}
		var got, exp any
		json.Unmarshal(calls[len(calls)-1].Params, &got)
		json.Unmarshal([]byte(want), &exp)
		g, _ := json.Marshal(got)
		e, _ := json.Marshal(exp)
		if string(g) != string(e) {
			t.Errorf("%s params = %s, want %s", method, g, e)
		}
	}

	noErr(t, c.SetBans(ctx, []rpc.Ban{{Host: "1.2.3.4", Ban: true, Seconds: 60}}))
	check("set_bans", `{"bans":[{"host":"1.2.3.4","ban":true,"seconds":60}]}`)
	must(c.SetLimit(ctx, -1, 512))
	check("set_limit", `{"limit_down":-1,"limit_up":512}`)
	must(c.OutPeers(ctx, 16))
	check("out_peers", `{"set":true,"out_peers":16}`)
	must(c.InPeers(ctx, 32))
	check("in_peers", `{"set":true,"in_peers":32}`)
	noErr(t, c.FlushTxpool(ctx, nil))
	check("flush_txpool", `{"txids":[]}`)
	noErr(t, c.RelayTx(ctx, []string{"aa"}))
	check("relay_tx", `{"txids":["aa"]}`)
	noErr(t, c.StartMining(ctx, rpc.StartMiningParams{MinerAddress: "4x", ThreadsCount: 2, DoBackgroundMining: true}))
	check("start_mining", `{"miner_address":"4x","threads_count":2,"do_background_mining":true,"ignore_battery":false}`)
	noErr(t, c.StopMining(ctx))
	noErr(t, c.SetLogHashRate(ctx, true))
	check("set_log_hash_rate", `{"visible":true}`)
	noErr(t, c.SetLogLevel(ctx, 2))
	check("set_log_level", `{"level":2}`)
	must(c.SetLogCategories(ctx, "*:WARNING"))
	check("set_log_categories", `{"categories":"*:WARNING"}`)
	noErr(t, c.SaveBC(ctx))
	noErr(t, c.FlushCache(ctx, true, false))
	check("flush_cache", `{"bad_txs":true,"bad_blocks":false}`)
	must(c.PopBlocks(ctx, 10))
	check("pop_blocks", `{"nblocks":10}`)
	noErr(t, c.StopDaemon(ctx))
	must(c.SendRawTransaction(ctx, "beef", true, true))
	check("send_raw_transaction", `{"tx_as_hex":"beef","do_not_relay":true,"do_sanity_checks":true}`)
	must(c.SubmitBlock(ctx, "cafe"))
	check("submit_block", `["cafe"]`)
	if h, _, err := c.GenerateBlocks(ctx, 10, "4x"); err != nil || h != 141 {
		t.Errorf("generateblocks: %d %v", h, err)
	}
	check("generateblocks", `{"amount_of_blocks":10,"wallet_address":"4x"}`)
	must(c.CalcPow(ctx, rpc.CalcPowParams{MajorVersion: 16, Height: 5, BlockBlob: "aa", SeedHash: "bb"}))
	check("calc_pow", `{"major_version":16,"height":5,"block_blob":"aa","seed_hash":"bb"}`)
}

// While syncing, monerod answers get_last_block_header with BUSY; the
// cached node falls back to fetching the top header by height.
func TestLastBlockHeaderWhileSyncing(t *testing.T) {
	d := rpctest.New(t)
	d.Handle("get_last_block_header", func(json.RawMessage) any { return map[string]string{"status": "BUSY"} })
	n := rpc.NewNode(rpc.New(rpc.Options{URL: d.URL}), time.Minute, time.Minute)
	h, err := n.GetLastBlockHeader(context.Background())
	if err != nil || h == nil || h.Hash == "" {
		t.Fatalf("header = %+v, err = %v", h, err)
	}
	calls := d.Calls("get_block_header_by_height")
	if len(calls) != 1 || !strings.Contains(string(calls[0].Params), `"height":130`) {
		t.Fatalf("fallback calls = %+v", calls)
	}
}
