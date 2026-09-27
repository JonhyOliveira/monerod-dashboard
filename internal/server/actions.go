package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
)

// An action changes the node's state. It validates its form input, calls
// monerod, and returns a message for the operator.
type action func(ctx context.Context, f form) (string, error)

// form reads validated values from a submitted form.
type form struct{ r *http.Request }

func (f form) str(k string) string { return strings.TrimSpace(f.r.PostFormValue(k)) }
func (f form) on(k string) bool    { return f.r.PostFormValue(k) != "" }
func (f form) all(k string) []string {
	var out []string
	for _, v := range f.r.PostForm[k] {
		for _, x := range fields(v) {
			out = append(out, strings.ToLower(x))
		}
	}
	return out
}

func (f form) uint(k string, maxV uint64) (uint64, error) {
	v := f.str(k)
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || n > maxV {
		return 0, fmt.Errorf("%s must be a whole number up to %s", strings.ReplaceAll(k, "_", " "), fmtInt(maxV))
	}
	return n, nil
}

// confirm checks a typed confirmation for destructive actions; the browser
// asks for it too, but the server is what enforces it.
func (f form) confirm(want string) error {
	if f.str("confirm") != want {
		return fmt.Errorf("type %q to confirm", want)
	}
	return nil
}

// userError marks validation errors, which are shown as-is.
type userError struct{ msg string }

func (e userError) Error() string { return e.msg }

func bad(format string, args ...any) error { return userError{fmt.Sprintf(format, args...)} }

func (s *Server) routeActions(mux *http.ServeMux) {
	actions := map[string]action{
		// Peers
		"ban":       s.actBan,
		"unban":     s.actUnban,
		"out_peers": s.actOutPeers,
		"in_peers":  s.actInPeers,
		// Network
		"set_limit": s.actSetLimit,
		// Mempool
		"flush_txpool": s.actFlushTxpool,
		"relay_tx":     s.actRelayTx,
		// Mining
		"start_mining":      s.actStartMining,
		"stop_mining":       s.actStopMining,
		"set_log_hash_rate": s.actSetLogHashRate,
		"generate_blocks":   s.actGenerateBlocks,
		"submit_block":      s.actSubmitBlock,
		// Maintenance
		"set_log_level":      s.actSetLogLevel,
		"set_log_categories": s.actSetLogCategories,
		"save_bc":            s.actSaveBC,
		"flush_cache":        s.actFlushCache,
		"prune_check":        s.actPruneCheck,
		"prune_blockchain":   s.actPrune,
		"pop_blocks":         s.actPopBlocks,
		"update_check":       s.actUpdateCheck,
		"update_download":    s.actUpdateDownload,
		"stop_daemon":        s.actStopDaemon,
	}
	for name, fn := range actions {
		mux.HandleFunc("POST /actions/"+name, s.runAction(name, fn))
	}
}

func (s *Server) runAction(name string, fn action) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form: "+err.Error(), http.StatusBadRequest)
			return
		}
		msg, err := fn(r.Context(), form{r})
		// Whatever the action changed, the next reads should show it.
		s.rpc.Invalidate()
		level := "ok"
		if err != nil {
			level = "error"
			msg = actionError(err)
		}
		log.Printf("action=%s ip=%s result=%s msg=%q", name, clientIP(r), level, msg)

		if r.Header.Get("HX-Request") == "true" {
			// The toast goes to #toasts; live sections refresh to show the effect.
			w.Header().Set("HX-Trigger", "live-refresh")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			var buf bytes.Buffer
			s.pages.pages["overview"].ExecuteTemplate(&buf, "toast", flash{Level: level, Message: msg})
			w.Write(buf.Bytes())
			return
		}
		// Without JavaScript: flash message and back to the page (POST/redirect/GET).
		if sess := sessionFrom(r); sess != nil {
			s.auth.addFlash(sess, level, msg)
		}
		http.Redirect(w, r, safeNext(r.PostFormValue("return")), http.StatusSeeOther)
	}
}

func actionError(err error) string {
	var ue userError
	if errors.As(err, &ue) {
		return ue.msg
	}
	return errMessage(err)
}

// ---- Peers ----

// validHost accepts an IP address or a CIDR subnet, as set_bans does.
func validHost(h string) bool {
	if net.ParseIP(h) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(h)
	return err == nil
}

func (s *Server) actBan(ctx context.Context, f form) (string, error) {
	host := f.str("host")
	if !validHost(host) {
		return "", bad("%q is not an IP address or subnet (e.g. 203.0.113.7 or 203.0.113.0/24)", host)
	}
	secs, err := f.uint("seconds", math.MaxUint32)
	if err != nil || secs == 0 {
		return "", bad("choose how long to ban for")
	}
	if err := s.rpc.SetBans(ctx, []rpc.Ban{{Host: host, Ban: true, Seconds: uint32(secs)}}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Banned %s for %s.", host, fmtDuration(int64(secs))), nil
}

func (s *Server) actUnban(ctx context.Context, f form) (string, error) {
	host := f.str("host")
	if !validHost(host) {
		return "", bad("%q is not an IP address or subnet", host)
	}
	if err := s.rpc.SetBans(ctx, []rpc.Ban{{Host: host, Ban: false}}); err != nil {
		return "", err
	}
	return "Unbanned " + host + ".", nil
}

func (s *Server) actOutPeers(ctx context.Context, f form) (string, error) {
	n, err := f.uint("limit", 1000)
	if err != nil {
		return "", err
	}
	got, err := s.rpc.OutPeers(ctx, uint32(n))
	if err != nil {
		return "", err
	}
	return "Outgoing connection limit set to " + limitText(got) + ".", nil
}

func (s *Server) actInPeers(ctx context.Context, f form) (string, error) {
	var n uint64 = math.MaxUint32
	if f.str("limit") != "" {
		var err error
		if n, err = f.uint("limit", 100000); err != nil {
			return "", err
		}
	}
	got, err := s.rpc.InPeers(ctx, uint32(n))
	if err != nil {
		return "", err
	}
	return "Incoming connection limit set to " + limitText(got) + ".", nil
}

// ---- Network ----

func (s *Server) actSetLimit(ctx context.Context, f form) (string, error) {
	var down, up int64 = -1, -1
	if !f.on("reset") {
		d, err := f.uint("limit_down", 1<<30)
		if err != nil {
			return "", err
		}
		u, err := f.uint("limit_up", 1<<30)
		if err != nil {
			return "", err
		}
		if d == 0 || u == 0 {
			return "", bad("limits must be at least 1 kB/s; use Reset for the defaults")
		}
		down, up = int64(d), int64(u)
	}
	l, err := s.rpc.SetLimit(ctx, down, up)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Bandwidth limits now %s kB/s down, %s kB/s up.", fmtInt(uint64(l.LimitDown)), fmtInt(uint64(l.LimitUp))), nil
}

// ---- Mempool ----

func hashes(f form, k string) ([]string, error) {
	hs := f.all(k)
	for _, h := range hs {
		if !rpc.IsHash(h) {
			return nil, bad("%q is not a transaction hash", h)
		}
	}
	return hs, nil
}

func (s *Server) actFlushTxpool(ctx context.Context, f form) (string, error) {
	ids, err := hashes(f, "txid")
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		if err := f.confirm("flush"); err != nil {
			return "", bad("select transactions to remove, or type \"flush\" to empty the whole pool")
		}
	}
	if err := s.rpc.FlushTxpool(ctx, ids); err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "Flushed the whole transaction pool.", nil
	}
	return fmt.Sprintf("Removed %d transaction(s) from the pool.", len(ids)), nil
}

func (s *Server) actRelayTx(ctx context.Context, f form) (string, error) {
	ids, err := hashes(f, "txid")
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", bad("select transactions to relay")
	}
	if err := s.rpc.RelayTx(ctx, ids); err != nil {
		return "", err
	}
	return fmt.Sprintf("Relayed %d transaction(s).", len(ids)), nil
}

// ---- Mining ----

// validAddress does a cheap shape check; monerod validates for real.
func validAddress(a string) bool {
	if len(a) != 95 && len(a) != 106 {
		return false
	}
	for _, c := range a {
		if !strings.ContainsRune("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz", c) {
			return false
		}
	}
	return true
}

func (s *Server) actStartMining(ctx context.Context, f form) (string, error) {
	addr := f.str("address")
	if !validAddress(addr) {
		return "", bad("enter a valid Monero address to mine to")
	}
	threads, err := f.uint("threads", 256)
	if err != nil || threads == 0 {
		return "", bad("threads must be between 1 and 256")
	}
	p := rpc.StartMiningParams{MinerAddress: addr, ThreadsCount: threads, DoBackgroundMining: f.on("background"), IgnoreBattery: f.on("ignore_battery")}
	if err := s.rpc.StartMining(ctx, p); err != nil {
		return "", err
	}
	return fmt.Sprintf("Mining started with %d thread(s).", threads), nil
}

func (s *Server) actStopMining(ctx context.Context, f form) (string, error) {
	if err := s.rpc.StopMining(ctx); err != nil {
		return "", err
	}
	return "Mining stopped.", nil
}

func (s *Server) actSetLogHashRate(ctx context.Context, f form) (string, error) {
	visible := f.str("visible") == "1"
	if err := s.rpc.SetLogHashRate(ctx, visible); err != nil {
		return "", err
	}
	if visible {
		return "The daemon now logs its hash rate.", nil
	}
	return "Hash-rate logging turned off.", nil
}

func (s *Server) actGenerateBlocks(ctx context.Context, f form) (string, error) {
	addr := f.str("address")
	if !validAddress(addr) {
		return "", bad("enter a valid address for the block rewards")
	}
	n, err := f.uint("count", 1000)
	if err != nil || n == 0 {
		return "", bad("generate between 1 and 1,000 blocks")
	}
	h, _, err := s.rpc.GenerateBlocks(ctx, n, addr)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Generated %d block(s); height is now %s.", n, fmtInt(h)), nil
}

func (s *Server) actSubmitBlock(ctx context.Context, f form) (string, error) {
	blob := f.str("blob")
	if blob == "" || !hexRE.MatchString(blob) || len(blob)%2 != 0 {
		return "", bad("the block blob must be hex")
	}
	id, err := s.rpc.SubmitBlock(ctx, blob)
	if err != nil {
		return "", err
	}
	return "Block accepted: " + id, nil
}

// ---- Maintenance ----

func (s *Server) actSetLogLevel(ctx context.Context, f form) (string, error) {
	lvl, err := f.uint("level", 4)
	if err != nil {
		return "", err
	}
	if err := s.rpc.SetLogLevel(ctx, int(lvl)); err != nil {
		return "", err
	}
	return fmt.Sprintf("Log level set to %d.", lvl), nil
}

var categoriesRE = regexp.MustCompile(`^[A-Za-z0-9_.*:,+\-]+$`)

func (s *Server) actSetLogCategories(ctx context.Context, f form) (string, error) {
	cats := f.str("categories")
	if !categoriesRE.MatchString(cats) {
		return "", bad("categories look like \"*:WARNING,net.p2p:DEBUG\"")
	}
	got, err := s.rpc.SetLogCategories(ctx, cats)
	if err != nil {
		return "", err
	}
	return "Log categories now: " + got, nil
}

func (s *Server) actSaveBC(ctx context.Context, f form) (string, error) {
	if err := s.rpc.SaveBC(ctx); err != nil {
		return "", err
	}
	return "Blockchain saved to disk.", nil
}

func (s *Server) actFlushCache(ctx context.Context, f form) (string, error) {
	txs, blocks := f.on("bad_txs"), f.on("bad_blocks")
	if !txs && !blocks {
		return "", bad("choose what to flush")
	}
	if err := s.rpc.FlushCache(ctx, txs, blocks); err != nil {
		return "", err
	}
	return "Cache flushed.", nil
}

// pruneOutcome describes a prune_blockchain answer.
func pruneOutcome(res *rpc.PruneResult) string {
	if res.Pruned {
		return fmt.Sprintf("The blockchain is pruned (pruning seed %d).", res.PruningSeed)
	}
	return "The blockchain is not pruned: the full chain is stored."
}

// Both run in the background: monerod verifies or rewrites the whole
// database, which takes minutes (check) to hours (prune) on mainnet.

func (s *Server) actPruneCheck(ctx context.Context, f form) (string, error) {
	ok := s.prune.start("Checking pruning", func(ctx context.Context) (string, error) {
		res, err := s.long.PruneBlockchain(ctx, true)
		if err != nil {
			return "", err
		}
		return pruneOutcome(res), nil
	})
	if !ok {
		return "", bad("a pruning check or prune is already running")
	}
	return "Checking the blockchain's pruning status. monerod reads the whole database for this, which can take several minutes; the result appears on the Maintenance page.", nil
}

func (s *Server) actPrune(ctx context.Context, f form) (string, error) {
	if err := f.confirm("prune"); err != nil {
		return "", bad("%s", err.Error())
	}
	ok := s.prune.start("Pruning", func(ctx context.Context) (string, error) {
		res, err := s.long.PruneBlockchain(ctx, false)
		if err != nil {
			return "", err
		}
		return pruneOutcome(res), nil
	})
	if !ok {
		return "", bad("a pruning check or prune is already running")
	}
	return "Pruning started. It can take hours on mainnet; progress is shown on the Maintenance page, and monerod keeps going if you close it.", nil
}

func (s *Server) actPopBlocks(ctx context.Context, f form) (string, error) {
	n, err := f.uint("count", 100000)
	if err != nil || n == 0 {
		return "", bad("enter how many blocks to remove (1 to 100,000)")
	}
	if err := f.confirm(strconv.FormatUint(n, 10)); err != nil {
		return "", bad("type the number of blocks (%d) to confirm", n)
	}
	h, err := s.rpc.PopBlocks(ctx, n)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Removed %d block(s); height is now %s.", n, fmtInt(h)), nil
}

func (s *Server) actUpdateCheck(ctx context.Context, f form) (string, error) {
	u, err := s.rpc.Update(ctx, "check")
	if err != nil {
		return "", err
	}
	if !u.Update {
		return "monerod is up to date.", nil
	}
	return fmt.Sprintf("Update available: v%s (%s).", u.Version, u.UserURI), nil
}

func (s *Server) actUpdateDownload(ctx context.Context, f form) (string, error) {
	if err := f.confirm("download"); err != nil {
		return "", bad("%s", err.Error())
	}
	u, err := s.rpc.Update(ctx, "download")
	if err != nil {
		return "", err
	}
	if !u.Update {
		return "No update to download.", nil
	}
	return fmt.Sprintf("Downloaded v%s to %s. Install it and restart monerod to finish.", u.Version, u.Path), nil
}

func (s *Server) actStopDaemon(ctx context.Context, f form) (string, error) {
	if err := f.confirm("stop"); err != nil {
		return "", bad("%s", err.Error())
	}
	if err := s.rpc.StopDaemon(ctx); err != nil {
		return "", err
	}
	return "monerod is shutting down. It will be unreachable until it is started again.", nil
}
