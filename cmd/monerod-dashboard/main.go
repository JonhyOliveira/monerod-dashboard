// Command monerod-dashboard serves a web dashboard to monitor and manage a
// monero daemon.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
	"github.com/jonhyoliveira/monerod-dashboard/internal/server"
)

func main() {
	var (
		rpcURL       = flag.String("rpc-url", envOr("MONEROD_RPC_URL", "http://127.0.0.1:18081"), "monerod RPC base URL (env MONEROD_RPC_URL)")
		rpcUser      = flag.String("rpc-user", os.Getenv("MONEROD_RPC_USER"), "RPC username for digest auth (env MONEROD_RPC_USER)")
		rpcPass      = flag.String("rpc-pass", os.Getenv("MONEROD_RPC_PASS"), "RPC password for digest auth (env MONEROD_RPC_PASS)")
		listen       = flag.String("listen", envOr("DASHBOARD_LISTEN", "127.0.0.1:8080"), "HTTP listen address (env DASHBOARD_LISTEN)")
		refresh      = flag.Duration("refresh", 5*time.Second, "refresh interval of live data (pages and the background cache)")
		slowRefresh  = flag.Duration("slow-refresh", time.Minute, "refresh interval of slow-changing data (peer lists, consensus, fees)")
		rpcTimeout   = flag.Duration("rpc-timeout", 30*time.Second, "timeout for each RPC request (some management calls are slow)")
		password     = flag.String("admin-password", os.Getenv("DASHBOARD_ADMIN_PASSWORD"), "password for the dashboard login (env DASHBOARD_ADMIN_PASSWORD)")
		passwordFile = flag.String("admin-password-file", os.Getenv("DASHBOARD_ADMIN_PASSWORD_FILE"), "file holding the dashboard password, e.g. a Docker secret (env DASHBOARD_ADMIN_PASSWORD_FILE)")
		noAuth       = flag.Bool("insecure-no-auth", false, "disable the login: anyone who can reach the dashboard can manage the node")
	)
	flag.Parse()

	pw, err := resolvePassword(*password, *passwordFile)
	if err != nil {
		log.Fatal(err)
	}
	if pw == "" && !*noAuth {
		log.Fatal("no dashboard password set: set DASHBOARD_ADMIN_PASSWORD (or DASHBOARD_ADMIN_PASSWORD_FILE), " +
			"or pass -insecure-no-auth to run without a login")
	}
	if *noAuth {
		log.Print("WARNING: login disabled (-insecure-no-auth); anyone who can reach the dashboard can manage the node")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Pages are served from a cache that this loop keeps fresh, so they
	// don't wait on monerod.
	client := rpc.New(rpc.Options{URL: *rpcURL, User: *rpcUser, Pass: *rpcPass, Timeout: *rpcTimeout})
	node := rpc.NewNode(client, *refresh, *slowRefresh)
	go node.Run(ctx)
	go node.Warm(ctx)

	srv := &http.Server{
		Addr:              *listen,
		Handler:           server.New(node, server.Config{Refresh: *refresh, Password: pw, NoAuth: *noAuth}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("monerod-dashboard listening on http://%s (daemon %s)", *listen, *rpcURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func resolvePassword(pw, file string) (string, error) {
	if file == "" {
		return pw, nil
	}
	if pw != "" {
		return "", errors.New("set either the admin password or the password file, not both")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("reading password file: %w", err)
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
