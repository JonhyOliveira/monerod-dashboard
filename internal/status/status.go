// Package status turns raw monerod RPC results into the dashboard's view
// model: derived values (sync progress, hashrate, uptime) and warnings.
package status

import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
)

// Thresholds for warnings.
const (
	MinFreeSpace      = 10 << 30 // 10 GiB
	MinFreeSpaceRatio = 0.05     // of database size
	MaxBlockAge       = 30 * time.Minute
	defaultTarget     = 120 // seconds, Monero's block target
)

// Node is the dashboard's view of a daemon.
type Node struct {
	State        string  `json:"state"` // "synchronized", "syncing" or "offline"
	StateLabel   string  `json:"state_label"`
	Height       uint64  `json:"height"`
	TargetHeight uint64  `json:"target_height"`
	BlocksBehind uint64  `json:"blocks_behind"`
	SyncPercent  float64 `json:"sync_percent"`

	Nettype         string `json:"nettype"`
	Version         string `json:"version"`
	UpdateAvailable bool   `json:"update_available"`
	Restricted      bool   `json:"restricted"`

	OutgoingPeers uint64 `json:"outgoing_peers"`
	IncomingPeers uint64 `json:"incoming_peers"`

	Difficulty    string  `json:"difficulty"` // decimal string, may exceed uint64
	Hashrate      float64 `json:"hashrate"`   // H/s
	TopBlockHash  string  `json:"top_block_hash"`
	LastBlockTime int64   `json:"last_block_time,omitempty"` // unix seconds
	LastBlockAgeS int64   `json:"last_block_age_seconds,omitempty"`
	TxPoolSize    uint64  `json:"tx_pool_size"`
	DatabaseSize  uint64  `json:"database_size"`
	FreeSpace     uint64  `json:"free_space"`
	StartTime     int64   `json:"start_time,omitempty"`
	UptimeSeconds int64   `json:"uptime_seconds,omitempty"`
}

// Warning is a condition the operator should look at.
type Warning struct {
	Level   string `json:"level"` // "warn" or "error"
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Build derives a Node and its warnings from get_info and (optionally) the
// last block header. now is injected for testability.
func Build(info *rpc.GetInfoResult, last *rpc.BlockHeader, now time.Time) (Node, []Warning) {
	n := Node{
		Height:          info.Height,
		TargetHeight:    info.TargetHeight,
		Nettype:         info.Nettype,
		Version:         info.Version,
		UpdateAvailable: info.UpdateAvailable,
		Restricted:      info.Restricted,
		OutgoingPeers:   info.OutgoingConnectionsCount,
		IncomingPeers:   info.IncomingConnectionsCount,
		TopBlockHash:    info.TopBlockHash,
		TxPoolSize:      info.TxPoolSize,
		DatabaseSize:    info.DatabaseSize,
		FreeSpace:       freeSpace(info),
		StartTime:       info.StartTime,
	}

	// get_info reports height as the number of blocks, target_height as the
	// best known height (0 when already synced).
	target := max(info.TargetHeight, info.Height)
	n.TargetHeight = target
	n.BlocksBehind = target - info.Height
	if target > 0 {
		n.SyncPercent = float64(info.Height) / float64(target) * 100
	}

	switch {
	case info.Offline:
		n.State, n.StateLabel = "offline", "Offline"
	case info.Synchronized && n.BlocksBehind == 0:
		n.State, n.StateLabel = "synchronized", "Synchronized"
	default:
		n.State = "syncing"
		n.StateLabel = fmt.Sprintf("Syncing (%d blocks behind)", n.BlocksBehind)
		if n.BlocksBehind == 1 {
			n.StateLabel = "Syncing (1 block behind)"
		}
	}

	diff := Difficulty(info)
	n.Difficulty = diff.String()
	blockTarget := info.Target
	if blockTarget == 0 {
		blockTarget = defaultTarget
	}
	hr, _ := new(big.Float).Quo(new(big.Float).SetInt(diff), big.NewFloat(float64(blockTarget))).Float64()
	n.Hashrate = hr

	if last != nil && last.Timestamp > 0 {
		n.LastBlockTime = last.Timestamp
		n.LastBlockAgeS = max(0, now.Unix()-last.Timestamp)
	}
	if info.StartTime > 0 {
		n.UptimeSeconds = max(0, now.Unix()-info.StartTime)
	}

	return n, warnings(n, info)
}

// freeSpace returns the daemon's free disk space, or 0 when unknown.
// Restricted RPC reports free_space as the maximum uint64 rather than 0.
func freeSpace(info *rpc.GetInfoResult) uint64 {
	if info.Restricted || info.FreeSpace == math.MaxUint64 {
		return 0
	}
	return info.FreeSpace
}

// Difficulty returns the network difficulty, preferring the 128-bit
// wide_difficulty hex string over the (possibly truncated) uint64 field.
func Difficulty(info *rpc.GetInfoResult) *big.Int {
	if s := strings.TrimPrefix(strings.ToLower(info.WideDifficulty), "0x"); s != "" {
		if d, ok := new(big.Int).SetString(s, 16); ok {
			return d
		}
	}
	return new(big.Int).SetUint64(info.Difficulty)
}

func warnings(n Node, info *rpc.GetInfoResult) []Warning {
	var ws []Warning
	add := func(level, code, msg string) {
		ws = append(ws, Warning{Level: level, Code: code, Message: msg})
	}

	if n.State == "offline" {
		add("error", "offline", "Daemon is running in offline mode.")
	}
	if !info.Offline && n.OutgoingPeers == 0 {
		add("error", "no_outgoing_peers", "No outgoing peer connections.")
	}
	// Only warn when free space is actually reported.
	if free := n.FreeSpace; free > 0 {
		if free < MinFreeSpace ||
			(info.DatabaseSize > 0 && float64(free) < MinFreeSpaceRatio*float64(info.DatabaseSize)) {
			add("warn", "low_disk", fmt.Sprintf("Low free disk space: %.1f GiB left.", float64(free)/(1<<30)))
		}
	}
	if n.LastBlockTime > 0 && n.State == "synchronized" && time.Duration(n.LastBlockAgeS)*time.Second > MaxBlockAge {
		add("warn", "stale_tip", fmt.Sprintf("Last block is %d minutes old.", n.LastBlockAgeS/60))
	}
	if n.UpdateAvailable {
		add("warn", "update_available", "A monerod update is available.")
	}
	return ws
}
