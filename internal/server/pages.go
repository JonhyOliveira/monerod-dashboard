package server

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
)

func (s *Server) routePages(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.pageOverview)
	mux.HandleFunc("GET /peers", s.pagePeers)
	mux.HandleFunc("GET /network", s.pageNetwork)
	mux.HandleFunc("GET /mempool", s.pageMempool)
	mux.HandleFunc("GET /blocks", s.pageBlocks)
	mux.HandleFunc("GET /block/{id}", s.pageBlock)
	mux.HandleFunc("GET /tx/{hash}", s.pageTx)
	mux.HandleFunc("GET /mining", s.pageMining)
	mux.HandleFunc("GET /maintenance", s.pageMaintenance)
	mux.HandleFunc("GET /tools", s.pageTools)
	mux.HandleFunc("POST /tools", s.pageTools)
	mux.HandleFunc("GET /console", s.pageConsole)
	mux.HandleFunc("POST /console", s.pageConsole)
}

// ---- Overview ----

type overviewData struct {
	Status   overview
	Net      result[*rpc.NetStats]
	Fee      result[*rpc.FeeEstimate]
	HardFork result[*rpc.HardForkInfoResult]
	Version  result[*rpc.GetVersionResult]
	Sync     result[*rpc.SyncInfoResult]
	Info     *rpc.GetInfoResult
}

func (s *Server) pageOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := s.Status(ctx)
	d := overviewData{Status: newOverview(st)}
	if st.OK {
		d.Info, _ = s.rpc.GetInfo(ctx)
		d.Net = try(s.rpc.GetNetStats(ctx))
		d.Fee = try(s.rpc.GetFeeEstimate(ctx))
		d.HardFork = try(s.rpc.HardForkInfo(ctx))
		d.Version = try(s.rpc.GetVersion(ctx))
		d.Sync = try(s.rpc.SyncInfo(ctx))
	}
	s.render(w, r, "overview", view{Title: "Overview", Live: true, Data: d})
}

// ---- Peers ----

type peersData struct {
	Connections result[[]rpc.Connection]
	Bans        result[[]rpc.Ban]
	PeerList    result[*rpc.PeerList]
	Public      result[*rpc.PublicNodes]
	OutLimit    uint32
	InLimit     uint32
	LimitsErr   error
}

func (s *Server) pagePeers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := peersData{
		Connections: try(s.rpc.GetConnections(ctx)),
		Bans:        try(s.rpc.GetBans(ctx)),
		PeerList:    try(s.rpc.GetPeerList(ctx)),
		Public:      try(s.rpc.GetPublicNodes(ctx)),
	}
	if d.Connections.Err == nil {
		// Results come from the shared cache: sort a copy.
		conns := slices.Clone(d.Connections.V)
		sort.SliceStable(conns, func(i, j int) bool { return conns[i].LiveTime > conns[j].LiveTime })
		d.Connections.V = conns
	}
	d.OutLimit, d.InLimit, d.LimitsErr = s.rpc.PeerLimits(ctx)
	s.render(w, r, "peers", view{Title: "Peers", Live: true, Data: d})
}

// limitText shows monerod's "no limit" sentinel as unlimited.
func limitText(n uint32) string {
	if n == math.MaxUint32 {
		return "unlimited"
	}
	return fmtInt(uint64(n))
}

// ---- Network ----

type networkData struct {
	Limits result[*rpc.Limits]
	Net    result[*rpc.NetStats]
	Sync   result[*rpc.SyncInfoResult]
	Now    int64
}

func (s *Server) pageNetwork(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := networkData{
		Limits: try(s.rpc.GetLimit(ctx)),
		Net:    try(s.rpc.GetNetStats(ctx)),
		Sync:   try(s.rpc.SyncInfo(ctx)),
		Now:    s.now().Unix(),
	}
	s.render(w, r, "network", view{Title: "Network", Live: true, Data: d})
}

// ---- Mempool ----

type mempoolData struct {
	Stats   result[*rpc.PoolStats]
	Pool    result[*rpc.TransactionPool]
	Backlog result[[]rpc.BacklogEntry]
	// Fee-rate summary of the backlog, piconero per byte.
	FeeRateMin, FeeRateMed, FeeRateMax uint64
	HistoMax                           uint32
}

func (s *Server) pageMempool(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := mempoolData{
		Stats:   try(s.rpc.GetTransactionPoolStats(ctx)),
		Pool:    try(s.rpc.GetTransactionPool(ctx)),
		Backlog: try(s.rpc.GetTxpoolBacklog(ctx)),
	}
	if d.Pool.Err == nil {
		// Results come from the shared cache: sort a copy.
		pool := *d.Pool.V
		pool.Transactions = slices.Clone(pool.Transactions)
		sort.SliceStable(pool.Transactions, func(i, j int) bool {
			return pool.Transactions[i].ReceiveTime > pool.Transactions[j].ReceiveTime
		})
		d.Pool.V = &pool
	}
	if d.Backlog.Err == nil && len(d.Backlog.V) > 0 {
		rates := make([]uint64, 0, len(d.Backlog.V))
		for _, b := range d.Backlog.V {
			if b.Weight > 0 {
				rates = append(rates, b.Fee/b.Weight)
			}
		}
		if len(rates) > 0 {
			sort.Slice(rates, func(i, j int) bool { return rates[i] < rates[j] })
			d.FeeRateMin, d.FeeRateMed, d.FeeRateMax = rates[0], rates[len(rates)/2], rates[len(rates)-1]
		}
	}
	if d.Stats.Err == nil {
		for _, h := range d.Stats.V.Histo {
			d.HistoMax = max(d.HistoMax, h.Txs)
		}
	}
	s.render(w, r, "mempool", view{Title: "Mempool", Live: true, Data: d})
}

// ---- Blocks ----

const blocksPerPage = 20

type blocksData struct {
	Height    uint64 // chain height (number of blocks)
	TopHash   string
	Start     uint64 // highest height shown
	Headers   result[[]rpc.BlockHeader]
	Newer     int64 // start of the newer page, -1 if none
	Older     int64 // start of the older page, -1 if none
	AltChains result[[]rpc.AltChain]
	AltBlocks result[[]string]
	Err       error
}

func (s *Server) pageBlocks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := blocksData{Newer: -1, Older: -1}
	h, err := s.rpc.GetHeight(ctx)
	if err != nil {
		d.Err = err
		s.render(w, r, "blocks", view{Title: "Blocks", Live: true, Data: d})
		return
	}
	d.Height, d.TopHash = h.Height, h.Hash
	top := h.Height - 1
	d.Start = top
	if v, err := strconv.ParseUint(r.URL.Query().Get("start"), 10, 64); err == nil && v < top {
		d.Start = v
	}
	end := d.Start
	begin := uint64(0)
	if end+1 > blocksPerPage {
		begin = end + 1 - blocksPerPage
	}
	d.Headers = try(s.rpc.GetBlockHeadersRange(ctx, begin, end))
	if d.Headers.Err == nil {
		// Newest first; reverse a copy of the shared cached slice.
		hs := slices.Clone(d.Headers.V)
		slices.Reverse(hs)
		d.Headers.V = hs
	}
	if d.Start < top {
		d.Newer = int64(min(top, d.Start+blocksPerPage))
	}
	if begin > 0 {
		d.Older = int64(begin - 1)
	}
	d.AltChains = try(s.rpc.GetAlternateChains(ctx))
	d.AltBlocks = try(s.rpc.GetAltBlocksHashes(ctx))
	// Only the newest page follows the chain tip.
	s.render(w, r, "blocks", view{Title: "Blocks", Live: d.Start == top, Data: d})
}

type blockData struct {
	Block   *rpc.Block
	Err     error
	HasNext bool
	Pretty  string
}

func (s *Server) pageBlock(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	var d blockData
	if n, err := strconv.ParseUint(id, 10, 64); err == nil {
		d.Block, d.Err = s.rpc.GetBlockByHeight(ctx, n)
	} else if rpc.IsHash(id) {
		d.Block, d.Err = s.rpc.GetBlockByHash(ctx, id)
	} else {
		d.Err = errors.New("not a block height or hash: " + id)
	}
	if d.Err == nil {
		d.HasNext = d.Block.BlockHeader.Depth > 0
		d.Pretty = rpc.PrettyJSON(d.Block.JSON)
	}
	s.render(w, r, "block", view{Title: "Block " + id, Data: d})
}

type txData struct {
	Hash    string
	Tx      *rpc.Transaction
	Pretty  string
	Summary txSummary
	Err     error
}

// txSummary is what the tx page pulls out of monerod's decoded JSON.
type txSummary struct {
	Version    int
	UnlockTime uint64
	Fee        uint64
	Inputs     []txInput
	Outputs    int
	ExtraLen   int
	Coinbase   bool
	RingSize   int
	RCTType    int
}

type txInput struct {
	KeyImage string
	RingSize int
}

func (s *Server) pageTx(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	d := txData{Hash: hash}
	if !rpc.IsHash(hash) {
		d.Err = errors.New("not a transaction hash")
	} else if txs, missed, err := s.rpc.GetTransactions(r.Context(), []string{hash}); err != nil {
		d.Err = err
	} else if len(txs) == 0 || len(missed) > 0 {
		d.Err = errors.New("transaction not found in the chain or the pool")
	} else {
		d.Tx = &txs[0]
		d.Pretty = rpc.PrettyJSON(d.Tx.AsJSON)
		d.Summary = summarizeTx(d.Tx.AsJSON)
	}
	s.render(w, r, "tx", view{Title: "Transaction " + shortHash(hash), Data: d})
}

func summarizeTx(asJSON string) txSummary {
	var tx struct {
		Version    int    `json:"version"`
		UnlockTime uint64 `json:"unlock_time"`
		Vin        []struct {
			Key *struct {
				KeyOffsets []uint64 `json:"key_offsets"`
				KImage     string   `json:"k_image"`
			} `json:"key"`
			Gen *struct{} `json:"gen"`
		} `json:"vin"`
		Vout  []json.RawMessage `json:"vout"`
		Extra []int             `json:"extra"`
		RCT   struct {
			Type   int    `json:"type"`
			TxnFee uint64 `json:"txnFee"`
		} `json:"rct_signatures"`
	}
	var sum txSummary
	if json.Unmarshal([]byte(asJSON), &tx) != nil {
		return sum
	}
	sum.Version, sum.UnlockTime, sum.Fee = tx.Version, tx.UnlockTime, tx.RCT.TxnFee
	sum.Outputs, sum.ExtraLen, sum.RCTType = len(tx.Vout), len(tx.Extra), tx.RCT.Type
	for _, in := range tx.Vin {
		if in.Gen != nil {
			sum.Coinbase = true
		}
		if in.Key != nil {
			sum.Inputs = append(sum.Inputs, txInput{KeyImage: in.Key.KImage, RingSize: len(in.Key.KeyOffsets)})
			sum.RingSize = len(in.Key.KeyOffsets)
		}
	}
	return sum
}

// handleSearch resolves a height, block hash or transaction hash.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	q = strings.ReplaceAll(q, ",", "")
	fail := func(msg string) {
		if sess := sessionFrom(r); sess != nil {
			s.auth.addFlash(sess, "error", msg)
		}
		back := "/blocks"
		if ref := r.Header.Get("Referer"); ref != "" {
			if i := strings.Index(ref, "://"); i >= 0 {
				if j := strings.Index(ref[i+3:], "/"); j >= 0 {
					back = safeNext(ref[i+3+j:])
				}
			}
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
	switch {
	case q == "":
		fail("Enter a block height, block hash or transaction hash.")
	case isDigits(q):
		http.Redirect(w, r, "/block/"+q, http.StatusSeeOther)
	case rpc.IsHash(q):
		if _, err := s.rpc.GetBlockHeaderByHash(r.Context(), q); err == nil {
			http.Redirect(w, r, "/block/"+q, http.StatusSeeOther)
			return
		}
		if txs, _, err := s.rpc.GetTransactions(r.Context(), []string{q}); err == nil && len(txs) > 0 {
			http.Redirect(w, r, "/tx/"+q, http.StatusSeeOther)
			return
		}
		fail("No block or transaction with hash " + q + ".")
	default:
		fail("\"" + q + "\" is not a block height or a 64-character hash.")
	}
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// ---- Mining ----

type miningData struct {
	Status    result[*rpc.MiningStatus]
	MinerData result[*rpc.MinerData]
	Info      *rpc.GetInfoResult
	Regtest   bool
	Template  *rpc.BlockTemplate
	TplErr    error
	TplAddr   string
}

func (s *Server) pageMining(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := miningData{
		Status:    try(s.rpc.GetMiningStatus(ctx)),
		MinerData: try(s.rpc.GetMinerData(ctx)),
	}
	if info, err := s.rpc.GetInfo(ctx); err == nil {
		d.Info = info
		d.Regtest = info.Nettype == "fakechain" || info.Nettype == "regtest"
	}
	if addr := strings.TrimSpace(r.URL.Query().Get("template_address")); addr != "" {
		d.TplAddr = addr
		reserve, _ := strconv.ParseUint(r.URL.Query().Get("reserve_size"), 10, 64)
		d.Template, d.TplErr = s.rpc.GetBlockTemplate(ctx, addr, min(reserve, 255))
	}
	s.render(w, r, "mining", view{Title: "Mining", Live: true, Data: d})
}

// ---- Maintenance ----

type maintenanceData struct {
	Info  *rpc.GetInfoResult
	Prune result[*rpc.PruneResult]
}

func (s *Server) pageMaintenance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := maintenanceData{Prune: try(s.rpc.PruneBlockchain(ctx, true))}
	d.Info, _ = s.rpc.GetInfo(ctx)
	s.render(w, r, "maintenance", view{Title: "Maintenance", Data: d})
}
