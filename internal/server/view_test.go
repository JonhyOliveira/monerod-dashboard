package server

import (
	"testing"
)

// Expected values are what the matching formatter in web/app.js returns.
func TestFormatters(t *testing.T) {
	bytesCases := map[uint64]string{
		0:          "—",
		512:        "512 B",
		1536:       "2 KiB",
		2560:       "3 KiB", // JS toFixed rounds ties up
		5 << 20:    "5 MiB",
		243 << 30:  "243.0 GiB",
		9663676416: "9.0 GiB",
		3 << 40:    "3.0 TiB",
	}
	for in, want := range bytesCases {
		if got := fmtBytes(in); got != want {
			t.Errorf("fmtBytes(%d) = %q, want %q", in, got, want)
		}
	}

	hashCases := map[float64]string{
		0:                  "—",
		950:                "950.00 H/s",
		3435726118.8416667: "3.44 GH/s",
		2.5e12:             "2.50 TH/s",
		4e15:               "4000.00 TH/s",
	}
	for in, want := range hashCases {
		if got := fmtHashrate(in); got != want {
			t.Errorf("fmtHashrate(%v) = %q, want %q", in, got, want)
		}
	}

	durCases := map[int64]string{
		0:      "0s",
		95:     "1m 35s",
		3600:   "1h 0m",
		277200: "3d 5h",
	}
	for in, want := range durCases {
		if got := fmtDuration(in); got != want {
			t.Errorf("fmtDuration(%d) = %q, want %q", in, got, want)
		}
	}

	intCases := map[uint64]string{0: "0", 999: "999", 1000: "1,000", 3212345: "3,212,345"}
	for in, want := range intCases {
		if got := fmtInt(in); got != want {
			t.Errorf("fmtInt(%d) = %q, want %q", in, got, want)
		}
	}

	bigCases := map[string]string{
		"":                     "—",
		"412287134261":         "412,287,134,261",
		"18446744073709551616": "18,446,744,073,709,551,616",
		"garbage":              "garbage",
	}
	for in, want := range bigCases {
		if got := fmtBigInt(in); got != want {
			t.Errorf("fmtBigInt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSyncPercentFloors(t *testing.T) {
	p := newPage(StatusResponse{OK: true, Node: &nodeFixture})
	if p.SyncPercent != "99.99%" {
		t.Errorf("SyncPercent = %q, want 99.99%%", p.SyncPercent)
	}
}
