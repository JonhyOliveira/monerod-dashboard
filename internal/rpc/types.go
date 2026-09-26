package rpc

// GetInfoResult is the subset of monerod's get_info response used by the
// dashboard. Fields that restricted RPC zeroes out (start_time, free_space,
// ...) are left as zero values.
type GetInfoResult struct {
	Status                   string `json:"status"`
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
	Difficulty               uint64 `json:"difficulty"`
	WideDifficulty           string `json:"wide_difficulty"`
	Target                   uint64 `json:"target"`
	TopBlockHash             string `json:"top_block_hash"`
	TxPoolSize               uint64 `json:"tx_pool_size"`
	DatabaseSize             uint64 `json:"database_size"`
	FreeSpace                uint64 `json:"free_space"`
	StartTime                int64  `json:"start_time"`
}

// BlockHeader is the subset of a block header used by the dashboard.
type BlockHeader struct {
	Height    uint64 `json:"height"`
	Hash      string `json:"hash"`
	Timestamp int64  `json:"timestamp"`
	NumTxes   uint64 `json:"num_txes"`
}

type getLastBlockHeaderResult struct {
	Status      string      `json:"status"`
	BlockHeader BlockHeader `json:"block_header"`
}
