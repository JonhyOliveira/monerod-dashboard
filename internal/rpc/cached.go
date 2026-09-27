package rpc

import (
	"context"
	"fmt"
	"time"
)

// Node is a Client whose read methods are answered from a cache that a
// background loop (Run) keeps fresh. Pages can call it freely: most calls
// return immediately with data at most one refresh interval old.
//
// Methods that change the daemon come from the embedded Client and are not
// cached; call Invalidate after them so the next reads show the effect.
// Results are shared between callers and must not be modified.
type Node struct {
	*Client
	cache      *cache
	fast, slow time.Duration
}

// NewNode wraps c. Live data (sync state, peers, mempool, mining) is
// refreshed every fast; slow-changing data (peer lists, consensus, version,
// fees) every slow.
func NewNode(c *Client, fast, slow time.Duration) *Node {
	return &Node{Client: c, cache: newCache(10*time.Minute, 60*time.Second), fast: fast, slow: slow}
}

// Run refreshes the cache until ctx is done.
func (n *Node) Run(ctx context.Context) {
	tick := min(n.fast/4, time.Second)
	n.cache.run(ctx, max(tick, 10*time.Millisecond))
}

// Invalidate drops every cached result.
func (n *Node) Invalidate() { n.cache.invalidate() }

// Warm fetches everything the overview shows, so the first page load after
// startup doesn't wait on the daemon, and there is last known data to show
// if it goes away later.
func (n *Node) Warm(ctx context.Context) {
	n.GetInfo(ctx)
	n.GetLastBlockHeader(ctx)
	n.GetNetStats(ctx)
	n.SyncInfo(ctx)
	n.GetFeeEstimate(ctx)
	n.HardForkInfo(ctx)
	n.GetVersion(ctx)
}

// cached is the typed front of cache.get. When a refresh failed but an
// earlier fetch succeeded, it returns that older value without an error;
// the context's Freshness (if any) records how old it is and why.
func cached[T any](ctx context.Context, n *Node, key string, every time.Duration, pinned bool, fetch func(context.Context) (T, error)) (T, time.Time, error) {
	r := n.cache.get(ctx, key, every, pinned, func(ctx context.Context) (any, error) { return fetch(ctx) })
	freshnessFrom(ctx).note(r)
	if !r.hasVal {
		var zero T
		return zero, time.Time{}, r.err
	}
	return r.val.(T), r.okAt, nil
}

// The overview's data is pinned: always kept fresh, even with nobody
// looking, so the dashboard opens instantly.

// GetInfo is Client.GetInfo, cached.
func (n *Node) GetInfo(ctx context.Context) (*GetInfoResult, error) {
	v, _, err := n.GetInfoAt(ctx)
	return v, err
}

// GetInfoAt is GetInfo plus the time the data was fetched (which is older
// than the last refresh if refreshes are failing).
func (n *Node) GetInfoAt(ctx context.Context) (*GetInfoResult, time.Time, error) {
	return cached(ctx, n, "get_info", n.fast, true, n.Client.GetInfo)
}

// GetLastBlockHeader is Client.GetLastBlockHeader, cached.
//
// monerod answers get_last_block_header with BUSY while it isn't
// synchronized; the same header is then fetched by height, which works
// during a sync.
func (n *Node) GetLastBlockHeader(ctx context.Context) (*BlockHeader, error) {
	v, _, err := cached(ctx, n, "get_last_block_header", n.fast, true, func(ctx context.Context) (*BlockHeader, error) {
		h, err := n.Client.GetLastBlockHeader(ctx)
		if !IsBusy(err) {
			return h, err
		}
		count, err := n.Client.GetBlockCount(ctx)
		if err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, fmt.Errorf("get_block_count: no blocks")
		}
		return n.Client.GetBlockHeaderByHeight(ctx, count-1)
	})
	return v, err
}

// GetNetStats is Client.GetNetStats, cached.
func (n *Node) GetNetStats(ctx context.Context) (*NetStats, error) {
	v, _, err := cached(ctx, n, "get_net_stats", n.fast, true, n.Client.GetNetStats)
	return v, err
}

// SyncInfo is Client.SyncInfo, cached.
func (n *Node) SyncInfo(ctx context.Context) (*SyncInfoResult, error) {
	v, _, err := cached(ctx, n, "sync_info", n.fast, true, n.Client.SyncInfo)
	return v, err
}

// GetFeeEstimate is Client.GetFeeEstimate, cached.
func (n *Node) GetFeeEstimate(ctx context.Context) (*FeeEstimate, error) {
	v, _, err := cached(ctx, n, "get_fee_estimate", n.slow, true, n.Client.GetFeeEstimate)
	return v, err
}

// HardForkInfo is Client.HardForkInfo, cached.
func (n *Node) HardForkInfo(ctx context.Context) (*HardForkInfoResult, error) {
	v, _, err := cached(ctx, n, "hard_fork_info", n.slow, true, n.Client.HardForkInfo)
	return v, err
}

// GetVersion is Client.GetVersion, cached.
func (n *Node) GetVersion(ctx context.Context) (*GetVersionResult, error) {
	v, _, err := cached(ctx, n, "get_version", n.slow, true, n.Client.GetVersion)
	return v, err
}

// Everything else stays fresh while it is being viewed.

// GetConnections is Client.GetConnections, cached.
func (n *Node) GetConnections(ctx context.Context) ([]Connection, error) {
	v, _, err := cached(ctx, n, "get_connections", n.fast, false, n.Client.GetConnections)
	return v, err
}

// GetBans is Client.GetBans, cached.
func (n *Node) GetBans(ctx context.Context) ([]Ban, error) {
	v, _, err := cached(ctx, n, "get_bans", n.fast, false, n.Client.GetBans)
	return v, err
}

// GetPeerList is Client.GetPeerList, cached.
func (n *Node) GetPeerList(ctx context.Context) (*PeerList, error) {
	v, _, err := cached(ctx, n, "get_peer_list", n.slow, false, n.Client.GetPeerList)
	return v, err
}

// GetPublicNodes is Client.GetPublicNodes, cached.
func (n *Node) GetPublicNodes(ctx context.Context) (*PublicNodes, error) {
	v, _, err := cached(ctx, n, "get_public_nodes", n.slow, false, n.Client.GetPublicNodes)
	return v, err
}

type peerLimits struct{ out, in uint32 }

// PeerLimits is Client.PeerLimits, cached.
func (n *Node) PeerLimits(ctx context.Context) (out, in uint32, err error) {
	v, _, err := cached(ctx, n, "peer_limits", n.slow, false, func(ctx context.Context) (peerLimits, error) {
		o, i, err := n.Client.PeerLimits(ctx)
		return peerLimits{o, i}, err
	})
	return v.out, v.in, err
}

// GetLimit is Client.GetLimit, cached.
func (n *Node) GetLimit(ctx context.Context) (*Limits, error) {
	v, _, err := cached(ctx, n, "get_limit", n.slow, false, n.Client.GetLimit)
	return v, err
}

// GetTransactionPool is Client.GetTransactionPool, cached.
func (n *Node) GetTransactionPool(ctx context.Context) (*TransactionPool, error) {
	v, _, err := cached(ctx, n, "get_transaction_pool", n.fast, false, n.Client.GetTransactionPool)
	return v, err
}

// GetTransactionPoolStats is Client.GetTransactionPoolStats, cached.
func (n *Node) GetTransactionPoolStats(ctx context.Context) (*PoolStats, error) {
	v, _, err := cached(ctx, n, "get_transaction_pool_stats", n.fast, false, n.Client.GetTransactionPoolStats)
	return v, err
}

// GetTxpoolBacklog is Client.GetTxpoolBacklog, cached.
func (n *Node) GetTxpoolBacklog(ctx context.Context) ([]BacklogEntry, error) {
	v, _, err := cached(ctx, n, "get_txpool_backlog", n.fast, false, n.Client.GetTxpoolBacklog)
	return v, err
}

// GetHeight is Client.GetHeight, cached.
func (n *Node) GetHeight(ctx context.Context) (*Height, error) {
	v, _, err := cached(ctx, n, "get_height", n.fast, false, n.Client.GetHeight)
	return v, err
}

// GetBlockHeadersRange is Client.GetBlockHeadersRange, cached per range.
func (n *Node) GetBlockHeadersRange(ctx context.Context, start, end uint64) ([]BlockHeader, error) {
	key := fmt.Sprintf("get_block_headers_range:%d-%d", start, end)
	v, _, err := cached(ctx, n, key, n.fast, false, func(ctx context.Context) ([]BlockHeader, error) {
		return n.Client.GetBlockHeadersRange(ctx, start, end)
	})
	return v, err
}

// GetAlternateChains is Client.GetAlternateChains, cached.
func (n *Node) GetAlternateChains(ctx context.Context) ([]AltChain, error) {
	v, _, err := cached(ctx, n, "get_alternate_chains", n.slow, false, n.Client.GetAlternateChains)
	return v, err
}

// GetAltBlocksHashes is Client.GetAltBlocksHashes, cached.
func (n *Node) GetAltBlocksHashes(ctx context.Context) ([]string, error) {
	v, _, err := cached(ctx, n, "get_alt_blocks_hashes", n.slow, false, n.Client.GetAltBlocksHashes)
	return v, err
}

// GetMiningStatus is Client.GetMiningStatus, cached.
func (n *Node) GetMiningStatus(ctx context.Context) (*MiningStatus, error) {
	v, _, err := cached(ctx, n, "mining_status", n.fast, false, n.Client.GetMiningStatus)
	return v, err
}

// GetMinerData is Client.GetMinerData, cached.
func (n *Node) GetMinerData(ctx context.Context) (*MinerData, error) {
	v, _, err := cached(ctx, n, "get_miner_data", n.fast, false, n.Client.GetMinerData)
	return v, err
}
