package rpc

import "context"

// GetInfoResult is monerod's get_info. Fields that restricted RPC hides
// (start_time, version, ...) are zero values there.
type GetInfoResult struct {
	Status
	Height                   uint64 `json:"height"`
	TargetHeight             uint64 `json:"target_height"`
	Synchronized             bool   `json:"synchronized"`
	BusySyncing              bool   `json:"busy_syncing"`
	Offline                  bool   `json:"offline"`
	Restricted               bool   `json:"restricted"`
	Nettype                  string `json:"nettype"`
	Version                  string `json:"version"`
	UpdateAvailable          bool   `json:"update_available"`
	OutgoingConnectionsCount uint64 `json:"outgoing_connections_count"`
	IncomingConnectionsCount uint64 `json:"incoming_connections_count"`
	RPCConnectionsCount      uint64 `json:"rpc_connections_count"`
	WhitePeerlistSize        uint64 `json:"white_peerlist_size"`
	GreyPeerlistSize         uint64 `json:"grey_peerlist_size"`
	Difficulty               uint64 `json:"difficulty"`
	WideDifficulty           string `json:"wide_difficulty"`
	WideCumulativeDifficulty string `json:"wide_cumulative_difficulty"`
	Target                   uint64 `json:"target"`
	TopBlockHash             string `json:"top_block_hash"`
	TxCount                  uint64 `json:"tx_count"`
	TxPoolSize               uint64 `json:"tx_pool_size"`
	AltBlocksCount           uint64 `json:"alt_blocks_count"`
	BlockSizeLimit           uint64 `json:"block_size_limit"`
	BlockSizeMedian          uint64 `json:"block_size_median"`
	BlockWeightLimit         uint64 `json:"block_weight_limit"`
	BlockWeightMedian        uint64 `json:"block_weight_median"`
	DatabaseSize             uint64 `json:"database_size"`
	FreeSpace                uint64 `json:"free_space"`
	StartTime                int64  `json:"start_time"`
	AdjustedTime             int64  `json:"adjusted_time"`
	BootstrapDaemonAddress   string `json:"bootstrap_daemon_address"`
	WasBootstrapEverUsed     bool   `json:"was_bootstrap_ever_used"`
	HeightWithoutBootstrap   uint64 `json:"height_without_bootstrap"`
}

// GetInfo calls get_info.
func (c *Client) GetInfo(ctx context.Context) (*GetInfoResult, error) {
	var r GetInfoResult
	return &r, c.Call(ctx, "get_info", nil, &r)
}

// GetVersionResult is get_version. Version packs major<<16 | minor.
type GetVersionResult struct {
	Status
	Version       uint32 `json:"version"`
	Release       bool   `json:"release"`
	CurrentHeight uint64 `json:"current_height"`
	HardForks     []struct {
		Height    uint64 `json:"height"`
		HFVersion uint8  `json:"hf_version"`
	} `json:"hard_forks"`
}

// GetVersion calls get_version.
func (c *Client) GetVersion(ctx context.Context) (*GetVersionResult, error) {
	var r GetVersionResult
	return &r, c.Call(ctx, "get_version", nil, &r)
}

// HardForkInfoResult is hard_fork_info.
type HardForkInfoResult struct {
	Status
	EarliestHeight uint64 `json:"earliest_height"`
	Enabled        bool   `json:"enabled"`
	State          uint32 `json:"state"`
	Threshold      uint32 `json:"threshold"`
	Version        uint8  `json:"version"`
	Votes          uint32 `json:"votes"`
	Voting         uint8  `json:"voting"`
	Window         uint32 `json:"window"`
}

// HardForkInfo calls hard_fork_info.
func (c *Client) HardForkInfo(ctx context.Context) (*HardForkInfoResult, error) {
	var r HardForkInfoResult
	return &r, c.Call(ctx, "hard_fork_info", nil, &r)
}

// SyncInfoResult is sync_info.
type SyncInfoResult struct {
	Status
	Height                uint64 `json:"height"`
	TargetHeight          uint64 `json:"target_height"`
	NextNeededPruningSeed uint32 `json:"next_needed_pruning_seed"`
	Overview              string `json:"overview"`
	Peers                 []struct {
		Info Connection `json:"info"`
	} `json:"peers"`
	Spans []struct {
		ConnectionID     string `json:"connection_id"`
		NBlocks          uint64 `json:"nblocks"`
		Rate             uint32 `json:"rate"`
		RemoteAddress    string `json:"remote_address"`
		Size             uint64 `json:"size"`
		Speed            uint32 `json:"speed"`
		StartBlockHeight uint64 `json:"start_block_height"`
	} `json:"spans"`
}

// SyncInfo calls sync_info.
func (c *Client) SyncInfo(ctx context.Context) (*SyncInfoResult, error) {
	var r SyncInfoResult
	return &r, c.Call(ctx, "sync_info", nil, &r)
}

// NetStats is /get_net_stats.
type NetStats struct {
	Status
	StartTime       int64  `json:"start_time"`
	TotalBytesIn    uint64 `json:"total_bytes_in"`
	TotalBytesOut   uint64 `json:"total_bytes_out"`
	TotalPacketsIn  uint64 `json:"total_packets_in"`
	TotalPacketsOut uint64 `json:"total_packets_out"`
}

// GetNetStats calls /get_net_stats.
func (c *Client) GetNetStats(ctx context.Context) (*NetStats, error) {
	var r NetStats
	return &r, c.CallPath(ctx, "/get_net_stats", nil, &r)
}

// Limits are bandwidth limits in kB/s.
type Limits struct {
	Status
	LimitDown int64 `json:"limit_down"`
	LimitUp   int64 `json:"limit_up"`
}

// GetLimit calls /get_limit.
func (c *Client) GetLimit(ctx context.Context) (*Limits, error) {
	var r Limits
	return &r, c.CallPath(ctx, "/get_limit", nil, &r)
}

// SetLimit calls /set_limit. For each direction, -1 resets to the default
// and 0 leaves it unchanged.
func (c *Client) SetLimit(ctx context.Context, down, up int64) (*Limits, error) {
	var r Limits
	return &r, c.CallPath(ctx, "/set_limit", map[string]int64{"limit_down": down, "limit_up": up}, &r)
}

// FeeEstimate is get_fee_estimate, fees are atomic units per byte.
type FeeEstimate struct {
	Status
	Fee              uint64   `json:"fee"`
	Fees             []uint64 `json:"fees"`
	QuantizationMask uint64   `json:"quantization_mask"`
}

// GetFeeEstimate calls get_fee_estimate.
func (c *Client) GetFeeEstimate(ctx context.Context) (*FeeEstimate, error) {
	var r FeeEstimate
	return &r, c.Call(ctx, "get_fee_estimate", nil, &r)
}

// UpdateResult is /update.
type UpdateResult struct {
	Status
	Update  bool   `json:"update"`
	Version string `json:"version"`
	UserURI string `json:"user_uri"`
	AutoURI string `json:"auto_uri"`
	Hash    string `json:"hash"`
	Path    string `json:"path"`
}

// Update calls /update with command "check" or "download".
func (c *Client) Update(ctx context.Context, command string) (*UpdateResult, error) {
	var r UpdateResult
	return &r, c.CallPath(ctx, "/update", map[string]string{"command": command}, &r)
}
