package status

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
)

var now = time.Unix(1_800_000_000, 0)

func healthy() *rpc.GetInfoResult {
	return &rpc.GetInfoResult{
		Status:                   "OK",
		Height:                   3_000_000,
		TargetHeight:             0,
		Synchronized:             true,
		Nettype:                  "mainnet",
		OutgoingConnectionsCount: 12,
		IncomingConnectionsCount: 30,
		Difficulty:               360_000_000_000,
		Target:                   120,
		DatabaseSize:             250 << 30,
		FreeSpace:                500 << 30,
		StartTime:                now.Unix() - 3600,
	}
}

func codes(ws []Warning) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Code)
	}
	return out
}

func TestBuildHealthy(t *testing.T) {
	n, ws := Build(healthy(), &rpc.BlockHeader{Timestamp: now.Unix() - 90}, now)
	if len(ws) != 0 {
		t.Errorf("unexpected warnings %v", codes(ws))
	}
	if n.State != "synchronized" || n.BlocksBehind != 0 || n.SyncPercent != 100 || n.TargetHeight != 3_000_000 {
		t.Errorf("sync fields: %+v", n)
	}
	if n.Hashrate != 3e9 {
		t.Errorf("hashrate = %v, want 3e9", n.Hashrate)
	}
	if n.UptimeSeconds != 3600 || n.LastBlockAgeS != 90 {
		t.Errorf("uptime=%d lastBlockAge=%d", n.UptimeSeconds, n.LastBlockAgeS)
	}
}

func TestSyncing(t *testing.T) {
	tests := []struct {
		height, target uint64
		synced         bool
		wantState      string
		wantBehind     uint64
		wantPct        float64
		wantLabel      string
	}{
		{1_500_000, 3_000_000, false, "syncing", 1_500_000, 50, "Syncing (1500000 blocks behind)"},
		{2_999_999, 3_000_000, false, "syncing", 1, 99.99996666667778, "Syncing (1 block behind)"},
		{3_000_000, 3_000_000, true, "synchronized", 0, 100, "Synchronized"},
		{0, 0, false, "syncing", 0, 0, "Syncing (0 blocks behind)"},
	}
	for _, tt := range tests {
		info := healthy()
		info.Height, info.TargetHeight, info.Synchronized = tt.height, tt.target, tt.synced
		n, _ := Build(info, nil, now)
		if n.State != tt.wantState || n.BlocksBehind != tt.wantBehind || math.Abs(n.SyncPercent-tt.wantPct) > 1e-9 || n.StateLabel != tt.wantLabel {
			t.Errorf("height=%d target=%d: got state=%s behind=%d pct=%v label=%q",
				tt.height, tt.target, n.State, n.BlocksBehind, n.SyncPercent, n.StateLabel)
		}
	}
}

func TestRestrictedFreeSpaceHidden(t *testing.T) {
	info := healthy()
	info.Restricted = true
	info.FreeSpace = math.MaxUint64 // what restricted RPC actually sends
	if n, _ := Build(info, nil, now); n.FreeSpace != 0 {
		t.Errorf("free space = %d, want 0 (unknown)", n.FreeSpace)
	}
}

func TestWideDifficulty(t *testing.T) {
	info := healthy()
	info.Difficulty = 1                         // truncated low 64 bits
	info.WideDifficulty = "0x10000000000000000" // 2^64
	n, _ := Build(info, nil, now)
	if n.Difficulty != "18446744073709551616" {
		t.Errorf("difficulty = %s", n.Difficulty)
	}
	if want := math.Pow(2, 64) / 120; math.Abs(n.Hashrate-want)/want > 1e-12 {
		t.Errorf("hashrate = %v, want %v", n.Hashrate, want)
	}
}

func TestWarnings(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*rpc.GetInfoResult)
		last   *rpc.BlockHeader
		want   []string
	}{
		{"no peers", func(i *rpc.GetInfoResult) { i.OutgoingConnectionsCount = 0 }, nil, []string{"no_outgoing_peers"}},
		{"low disk absolute", func(i *rpc.GetInfoResult) { i.FreeSpace = 5 << 30; i.DatabaseSize = 50 << 30 }, nil, []string{"low_disk"}},
		{"low disk ratio", func(i *rpc.GetInfoResult) { i.FreeSpace = 12 << 30; i.DatabaseSize = 300 << 30 }, nil, []string{"low_disk"}},
		{"restricted hides disk", func(i *rpc.GetInfoResult) { i.FreeSpace = 1; i.Restricted = true }, nil, nil},
		{"free space unreported", func(i *rpc.GetInfoResult) { i.FreeSpace = 0 }, nil, nil},
		{"free space hidden as max uint64", func(i *rpc.GetInfoResult) { i.FreeSpace = math.MaxUint64 }, nil, nil},
		{"stale tip", func(i *rpc.GetInfoResult) {}, &rpc.BlockHeader{Timestamp: now.Unix() - 3600}, []string{"stale_tip"}},
		{"stale tip ignored while syncing", func(i *rpc.GetInfoResult) { i.TargetHeight = i.Height + 100 }, &rpc.BlockHeader{Timestamp: now.Unix() - 3600}, nil},
		{"update", func(i *rpc.GetInfoResult) { i.UpdateAvailable = true }, nil, []string{"update_available"}},
		{"offline", func(i *rpc.GetInfoResult) { i.Offline = true; i.OutgoingConnectionsCount = 0 }, nil, []string{"offline"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := healthy()
			tt.modify(info)
			_, ws := Build(info, tt.last, now)
			if got := codes(ws); !slices.Equal(got, tt.want) {
				t.Errorf("warnings = %v, want %v", got, tt.want)
			}
		})
	}
}
