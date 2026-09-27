package rpc

import "context"

// SetLogLevel calls /set_log_level (0-4).
func (c *Client) SetLogLevel(ctx context.Context, level int) error {
	var r Status
	return c.CallPath(ctx, "/set_log_level", map[string]int{"level": level}, &r)
}

// SetLogCategories calls /set_log_categories and returns the categories now
// in effect. An empty string only reads them.
func (c *Client) SetLogCategories(ctx context.Context, categories string) (string, error) {
	var r struct {
		Status
		Categories string `json:"categories"`
	}
	var req any
	if categories != "" {
		req = map[string]string{"categories": categories}
	}
	return r.Categories, c.CallPath(ctx, "/set_log_categories", req, &r)
}

// SaveBC calls /save_bc to flush the blockchain to disk.
func (c *Client) SaveBC(ctx context.Context) error {
	var r Status
	return c.CallPath(ctx, "/save_bc", nil, &r)
}

// FlushCache calls flush_cache to forget known bad transactions and/or blocks.
func (c *Client) FlushCache(ctx context.Context, badTxs, badBlocks bool) error {
	var r Status
	return c.Call(ctx, "flush_cache", map[string]bool{"bad_txs": badTxs, "bad_blocks": badBlocks}, &r)
}

// PruneResult is prune_blockchain.
type PruneResult struct {
	Status
	Pruned      bool   `json:"pruned"`
	PruningSeed uint32 `json:"pruning_seed"`
}

// PruneBlockchain calls prune_blockchain. With check it only reports the
// current state; otherwise it prunes, which cannot be undone.
func (c *Client) PruneBlockchain(ctx context.Context, check bool) (*PruneResult, error) {
	var r PruneResult
	return &r, c.Call(ctx, "prune_blockchain", map[string]bool{"check": check}, &r)
}

// PopBlocks calls /pop_blocks and returns the new height.
func (c *Client) PopBlocks(ctx context.Context, n uint64) (uint64, error) {
	var r struct {
		Status
		Height uint64 `json:"height"`
	}
	return r.Height, c.CallPath(ctx, "/pop_blocks", map[string]uint64{"nblocks": n}, &r)
}

// StopDaemon calls /stop_daemon.
func (c *Client) StopDaemon(ctx context.Context) error {
	var r Status
	return c.CallPath(ctx, "/stop_daemon", nil, &r)
}
