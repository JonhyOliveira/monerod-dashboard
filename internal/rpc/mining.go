package rpc

import "context"

// MiningStatus is /mining_status.
type MiningStatus struct {
	Status
	Active                    bool   `json:"active"`
	Address                   string `json:"address"`
	Speed                     uint64 `json:"speed"`
	ThreadsCount              uint32 `json:"threads_count"`
	IsBackgroundMiningEnabled bool   `json:"is_background_mining_enabled"`
	BgIdleThreshold           uint8  `json:"bg_idle_threshold"`
	BgMinIdleSeconds          uint8  `json:"bg_min_idle_seconds"`
	BgIgnoreBattery           bool   `json:"bg_ignore_battery"`
	BgTarget                  uint8  `json:"bg_target"`
	BlockReward               uint64 `json:"block_reward"`
	BlockTarget               uint32 `json:"block_target"`
	WideDifficulty            string `json:"wide_difficulty"`
	PowAlgorithm              string `json:"pow_algorithm"`
}

// GetMiningStatus calls /mining_status.
func (c *Client) GetMiningStatus(ctx context.Context) (*MiningStatus, error) {
	var r MiningStatus
	return &r, c.CallPath(ctx, "/mining_status", nil, &r)
}

// StartMiningParams are /start_mining's parameters.
type StartMiningParams struct {
	MinerAddress       string `json:"miner_address"`
	ThreadsCount       uint64 `json:"threads_count"`
	DoBackgroundMining bool   `json:"do_background_mining"`
	IgnoreBattery      bool   `json:"ignore_battery"`
}

// StartMining calls /start_mining.
func (c *Client) StartMining(ctx context.Context, p StartMiningParams) error {
	var r Status
	return c.CallPath(ctx, "/start_mining", p, &r)
}

// StopMining calls /stop_mining.
func (c *Client) StopMining(ctx context.Context) error {
	var r Status
	return c.CallPath(ctx, "/stop_mining", nil, &r)
}

// SetLogHashRate calls /set_log_hash_rate; the daemon must be mining.
func (c *Client) SetLogHashRate(ctx context.Context, visible bool) error {
	var r Status
	return c.CallPath(ctx, "/set_log_hash_rate", map[string]bool{"visible": visible}, &r)
}

// MinerData is get_miner_data.
type MinerData struct {
	Status
	MajorVersion          uint8  `json:"major_version"`
	Height                uint64 `json:"height"`
	PrevID                string `json:"prev_id"`
	SeedHash              string `json:"seed_hash"`
	Difficulty            string `json:"difficulty"`
	MedianWeight          uint64 `json:"median_weight"`
	AlreadyGeneratedCoins uint64 `json:"already_generated_coins"`
	TxBacklog             []struct {
		ID     string `json:"id"`
		Weight uint64 `json:"weight"`
		Fee    uint64 `json:"fee"`
	} `json:"tx_backlog"`
}

// GetMinerData calls get_miner_data.
func (c *Client) GetMinerData(ctx context.Context) (*MinerData, error) {
	var r MinerData
	return &r, c.Call(ctx, "get_miner_data", nil, &r)
}

// BlockTemplate is get_block_template.
type BlockTemplate struct {
	Status
	BlockhashingBlob  string `json:"blockhashing_blob"`
	BlocktemplateBlob string `json:"blocktemplate_blob"`
	WideDifficulty    string `json:"wide_difficulty"`
	ExpectedReward    uint64 `json:"expected_reward"`
	Height            uint64 `json:"height"`
	PrevHash          string `json:"prev_hash"`
	ReservedOffset    uint64 `json:"reserved_offset"`
	SeedHash          string `json:"seed_hash"`
	SeedHeight        uint64 `json:"seed_height"`
	NextSeedHash      string `json:"next_seed_hash"`
}

// GetBlockTemplate calls get_block_template.
func (c *Client) GetBlockTemplate(ctx context.Context, address string, reserveSize uint64) (*BlockTemplate, error) {
	var r BlockTemplate
	return &r, c.Call(ctx, "get_block_template", map[string]any{"wallet_address": address, "reserve_size": reserveSize}, &r)
}

// SubmitBlock calls submit_block with a block blob in hex.
func (c *Client) SubmitBlock(ctx context.Context, blobHex string) (string, error) {
	var r struct {
		Status
		BlockID string `json:"block_id"`
	}
	return r.BlockID, c.Call(ctx, "submit_block", []string{blobHex}, &r)
}

// CalcPowParams are calc_pow's parameters.
type CalcPowParams struct {
	MajorVersion uint8  `json:"major_version"`
	Height       uint64 `json:"height"`
	BlockBlob    string `json:"block_blob"`
	SeedHash     string `json:"seed_hash"`
}

// CalcPow calls calc_pow and returns the PoW hash.
func (c *Client) CalcPow(ctx context.Context, p CalcPowParams) (string, error) {
	var hash string
	return hash, c.Call(ctx, "calc_pow", p, &hash)
}

// GenerateBlocks calls generateblocks (regtest/fakechain only).
func (c *Client) GenerateBlocks(ctx context.Context, n uint64, address string) (height uint64, blocks []string, err error) {
	var r struct {
		Status
		Height uint64   `json:"height"`
		Blocks []string `json:"blocks"`
	}
	err = c.Call(ctx, "generateblocks", map[string]any{"amount_of_blocks": n, "wallet_address": address}, &r)
	return r.Height, r.Blocks, err
}
