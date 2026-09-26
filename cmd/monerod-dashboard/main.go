// Command monerod-dashboard serves a web dashboard for a monero daemon.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jonhyoliveira/monerod-dashboard/internal/rpc"
	"github.com/jonhyoliveira/monerod-dashboard/internal/server"
)

func main() {
	var (
		rpcURL     = flag.String("rpc-url", envOr("MONEROD_RPC_URL", "http://127.0.0.1:18081"), "monerod RPC base URL (env MONEROD_RPC_URL)")
		rpcUser    = flag.String("rpc-user", os.Getenv("MONEROD_RPC_USER"), "RPC username for digest auth (env MONEROD_RPC_USER)")
		rpcPass    = flag.String("rpc-pass", os.Getenv("MONEROD_RPC_PASS"), "RPC password for digest auth (env MONEROD_RPC_PASS)")
		listen     = flag.String("listen", envOr("DASHBOARD_LISTEN", "127.0.0.1:8080"), "HTTP listen address (env DASHBOARD_LISTEN)")
		refresh    = flag.Duration("refresh", 5*time.Second, "UI refresh interval")
		rpcTimeout = flag.Duration("rpc-timeout", 5*time.Second, "timeout for each RPC request")
	)
	flag.Parse()

	client := rpc.New(rpc.Options{URL: *rpcURL, User: *rpcUser, Pass: *rpcPass, Timeout: *rpcTimeout})
	srv := &http.Server{
		Addr:              *listen,
		Handler:           server.New(client, *refresh).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
