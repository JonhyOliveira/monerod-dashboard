package server

import (
	"fmt"
	"html/template"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/jonhyoliveira/monerod-dashboard/internal/status"
)

// Formatting lives only here: pages are rendered on the server, and live
// refreshes swap in server-rendered HTML.

const dash = "—"

// overview is the view model for the overview page's status cards.
type overview struct {
	OK       bool
	Warnings []status.Warning

	StateClass   string
	StateLabel   string
	SyncBarClass string
	SyncBarStyle template.CSS
	Height       string
	TargetHeight string
	SyncPercent  string

	PeersOut string
	PeersIn  string

	Hashrate   string
	Difficulty string
	LastBlock  string
	TopHash    string

	TxPool string

	DBSize       string
	FreeSpace    string
	DiskBarClass string
	DiskBarStyle template.CSS

	Uptime    string
	StartTime int64
}

func newOverview(s StatusResponse) overview {
	p := overview{
		OK:       s.OK,
		Warnings: s.Warnings,

		StateClass:   "unreachable",
		StateLabel:   "Unreachable",
		SyncBarStyle: "width: 0%",
		DiskBarStyle: "width: 0%",
		Height:       dash, TargetHeight: dash, SyncPercent: dash,
		PeersOut: dash, PeersIn: dash,
		Hashrate: dash, Difficulty: dash, LastBlock: dash,
		TxPool: dash,
		DBSize: dash, FreeSpace: dash,
		Uptime: dash,
	}
	n := s.Node
	if !s.OK || n == nil {
		return p
	}

	p.StateClass = n.State
	p.StateLabel = n.StateLabel
	pct := math.Min(100, n.SyncPercent)
	p.SyncBarStyle = template.CSS("width: " + strconv.FormatFloat(pct, 'f', -1, 64) + "%")
	p.SyncBarClass = "warn"
	if n.State == "synchronized" {
		p.SyncBarClass = "ok"
	}
	p.Height = fmtInt(n.Height)
	p.TargetHeight = fmtInt(n.TargetHeight)
	// Floor so a node that is still behind never reads "100.00%".
	p.SyncPercent = toFixed(math.Floor(pct*100)/100, 2) + "%"

	p.PeersOut = fmtInt(n.OutgoingPeers)
	p.PeersIn = fmtInt(n.IncomingPeers)

	p.Hashrate = fmtHashrate(n.Hashrate)
	p.Difficulty = fmtBigInt(n.Difficulty)
	if n.LastBlockTime != 0 {
		p.LastBlock = fmtDuration(n.LastBlockAgeS) + " ago"
	}
	p.TopHash = n.TopBlockHash

	p.TxPool = fmtInt(n.TxPoolSize)

	p.DBSize = fmtBytes(n.DatabaseSize)
	p.FreeSpace = fmtBytes(n.FreeSpace)
	if n.FreeSpace != 0 && n.DatabaseSize != 0 {
		// Share of (db + free) in use; a rough proxy for headroom.
		used := 100 - float64(n.FreeSpace)/float64(n.FreeSpace+n.DatabaseSize)*100
		p.DiskBarStyle = template.CSS("width: " + strconv.FormatFloat(used, 'f', -1, 64) + "%")
		p.DiskBarClass = "ok"
		for _, w := range s.Warnings {
			if w.Code == "low_disk" {
				p.DiskBarClass = "err"
			}
		}
	}

	if n.UptimeSeconds != 0 {
		p.Uptime = fmtDuration(n.UptimeSeconds)
	}
	p.StartTime = n.StartTime
	return p
}

// toFixed matches JavaScript's Number.prototype.toFixed for non-negative
// values, which rounds ties up where strconv rounds half to even.
func toFixed(v float64, digits int) string {
	pow := math.Pow(10, float64(digits))
	return strconv.FormatFloat(math.Floor(v*pow+0.5)/pow, 'f', digits, 64)
}

func fmtBytes(n uint64) string {
	if n == 0 {
		return dash
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	digits := 0
	if i >= 3 {
		digits = 1
	}
	return toFixed(v, digits) + " " + units[i]
}

func fmtHashrate(h float64) string {
	if h == 0 {
		return dash
	}
	units := []string{"H/s", "kH/s", "MH/s", "GH/s", "TH/s"}
	i := 0
	for h >= 1000 && i < len(units)-1 {
		h /= 1000
		i++
	}
	return toFixed(h, 2) + " " + units[i]
}

func fmtDuration(s int64) string {
	d, h, m, sec := s/86400, s%86400/3600, s%3600/60, s%60
	switch {
	case d != 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h != 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m != 0:
		return fmt.Sprintf("%dm %ds", m, sec)
	}
	return fmt.Sprintf("%ds", sec)
}

func fmtInt(n uint64) string { return groupThousands(strconv.FormatUint(n, 10)) }

// fmtBigInt formats a decimal integer string with thousands separators.
func fmtBigInt(s string) string {
	if s == "" {
		return dash
	}
	if _, ok := new(big.Int).SetString(s, 10); !ok {
		return s
	}
	return groupThousands(s)
}

func groupThousands(digits string) string {
	neg := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
