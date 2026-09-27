package rpc

import (
	"context"
	"encoding/json"
)

// BlockHeader is a block header as returned by the get_block* methods.
type BlockHeader struct {
	Hash                     string `json:"hash"`
	Height                   uint64 `json:"height"`
	PrevHash                 string `json:"prev_hash"`
	Timestamp                int64  `json:"timestamp"`
	MajorVersion             uint8  `json:"major_version"`
	MinorVersion             uint8  `json:"minor_version"`
	Nonce                    uint32 `json:"nonce"`
	NumTxes                  uint64 `json:"num_txes"`
	Reward                   uint64 `json:"reward"`
	BlockSize                uint64 `json:"block_size"`
	BlockWeight              uint64 `json:"block_weight"`
	LongTermWeight           uint64 `json:"long_term_weight"`
	Depth                    uint64 `json:"depth"`
	Difficulty               uint64 `json:"difficulty"`
	WideDifficulty           string `json:"wide_difficulty"`
	WideCumulativeDifficulty string `json:"wide_cumulative_difficulty"`
	MinerTxHash              string `json:"miner_tx_hash"`
	OrphanStatus             bool   `json:"orphan_status"`
	PowHash                  string `json:"pow_hash"`
}

type headerResult struct {
	Status
	BlockHeader BlockHeader `json:"block_header"`
}

// GetLastBlockHeader calls get_last_block_header.
func (c *Client) GetLastBlockHeader(ctx context.Context) (*BlockHeader, error) {
	var r headerResult
	return &r.BlockHeader, c.Call(ctx, "get_last_block_header", nil, &r)
}

// GetBlockHeaderByHash calls get_block_header_by_hash.
func (c *Client) GetBlockHeaderByHash(ctx context.Context, hash string) (*BlockHeader, error) {
	var r headerResult
	return &r.BlockHeader, c.Call(ctx, "get_block_header_by_hash", map[string]string{"hash": hash}, &r)
}

// GetBlockHeaderByHeight calls get_block_header_by_height.
func (c *Client) GetBlockHeaderByHeight(ctx context.Context, height uint64) (*BlockHeader, error) {
	var r headerResult
	return &r.BlockHeader, c.Call(ctx, "get_block_header_by_height", map[string]uint64{"height": height}, &r)
}

// GetBlockHeadersRange calls get_block_headers_range (inclusive range).
func (c *Client) GetBlockHeadersRange(ctx context.Context, start, end uint64) ([]BlockHeader, error) {
	var r struct {
		Status
		Headers []BlockHeader `json:"headers"`
	}
	err := c.Call(ctx, "get_block_headers_range", map[string]uint64{"start_height": start, "end_height": end}, &r)
	return r.Headers, err
}

// Block is get_block. JSON is the block's decoded structure as monerod
// prints it.
type Block struct {
	Status
	Blob        string      `json:"blob"`
	BlockHeader BlockHeader `json:"block_header"`
	JSON        string      `json:"json"`
	MinerTxHash string      `json:"miner_tx_hash"`
	TxHashes    []string    `json:"tx_hashes"`
}

// GetBlockByHeight calls get_block with a height.
func (c *Client) GetBlockByHeight(ctx context.Context, height uint64) (*Block, error) {
	var r Block
	return &r, c.Call(ctx, "get_block", map[string]uint64{"height": height}, &r)
}

// GetBlockByHash calls get_block with a hash.
func (c *Client) GetBlockByHash(ctx context.Context, hash string) (*Block, error) {
	var r Block
	return &r, c.Call(ctx, "get_block", map[string]string{"hash": hash}, &r)
}

// GetBlockCount calls get_block_count.
func (c *Client) GetBlockCount(ctx context.Context) (uint64, error) {
	var r struct {
		Status
		Count uint64 `json:"count"`
	}
	return r.Count, c.Call(ctx, "get_block_count", nil, &r)
}

// GetBlockHash calls on_get_block_hash.
func (c *Client) GetBlockHash(ctx context.Context, height uint64) (string, error) {
	var hash string
	return hash, c.Call(ctx, "on_get_block_hash", []uint64{height}, &hash)
}

// Height is /get_height.
type Height struct {
	Status
	Height uint64 `json:"height"`
	Hash   string `json:"hash"`
}

// GetHeight calls /get_height.
func (c *Client) GetHeight(ctx context.Context) (*Height, error) {
	var r Height
	return &r, c.CallPath(ctx, "/get_height", nil, &r)
}

// AltChain is one entry of get_alternate_chains.
type AltChain struct {
	BlockHash            string   `json:"block_hash"`
	BlockHashes          []string `json:"block_hashes"`
	Height               uint64   `json:"height"`
	Length               uint64   `json:"length"`
	MainChainParentBlock string   `json:"main_chain_parent_block"`
	WideDifficulty       string   `json:"wide_difficulty"`
}

// GetAlternateChains calls get_alternate_chains.
func (c *Client) GetAlternateChains(ctx context.Context) ([]AltChain, error) {
	var r struct {
		Status
		Chains []AltChain `json:"chains"`
	}
	return r.Chains, c.Call(ctx, "get_alternate_chains", nil, &r)
}

// GetAltBlocksHashes calls /get_alt_blocks_hashes.
func (c *Client) GetAltBlocksHashes(ctx context.Context) ([]string, error) {
	var r struct {
		Status
		BlksHashes []string `json:"blks_hashes"`
	}
	return r.BlksHashes, c.CallPath(ctx, "/get_alt_blocks_hashes", nil, &r)
}

// CoinbaseTxSum is get_coinbase_tx_sum; wide fields are 128-bit hex.
type CoinbaseTxSum struct {
	Status
	EmissionAmount     uint64 `json:"emission_amount"`
	FeeAmount          uint64 `json:"fee_amount"`
	WideEmissionAmount string `json:"wide_emission_amount"`
	WideFeeAmount      string `json:"wide_fee_amount"`
}

// GetCoinbaseTxSum calls get_coinbase_tx_sum over [height, height+count).
func (c *Client) GetCoinbaseTxSum(ctx context.Context, height, count uint64) (*CoinbaseTxSum, error) {
	var r CoinbaseTxSum
	return &r, c.Call(ctx, "get_coinbase_tx_sum", map[string]uint64{"height": height, "count": count}, &r)
}

// PrettyJSON re-indents monerod's embedded JSON strings for display.
func PrettyJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return s
	}
	return string(out)
}
