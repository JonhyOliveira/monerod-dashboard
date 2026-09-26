# monerod-dashboard

A dashboard for the monero daemon. Exposes some of the information available through the RPC interface.

A single Go binary (standard library only) that queries `monerod`'s JSON-RPC and serves a web page showing whether your node is healthy and in sync:

- **Sync**: height vs. target height, progress, synchronized / syncing / offline
- **Peers**: outgoing and incoming connections
- **Chain**: network hashrate, difficulty, last block age, top block hash
- **Mempool**: pending transaction count
- **Storage**: database size and free disk space
- **Uptime**
- **Warnings**: unreachable daemon, no outgoing peers, low disk space, stale chain tip, update available

The dashboard server proxies the RPC, so the browser never talks to `monerod` directly and RPC credentials stay on the server.

## Build & run

Requires Go 1.24+.

```sh
go build -o monerod-dashboard ./cmd/monerod-dashboard
./monerod-dashboard -rpc-url http://127.0.0.1:18081
```

Then open http://127.0.0.1:8080.

## Configuration

| Flag           | Env var            | Default                  | Description                                  |
|----------------|--------------------|--------------------------|----------------------------------------------|
| `-rpc-url`     | `MONEROD_RPC_URL`  | `http://127.0.0.1:18081` | monerod RPC base URL                         |
| `-rpc-user`    | `MONEROD_RPC_USER` |                          | Username for `--rpc-login` (digest auth)     |
| `-rpc-pass`    | `MONEROD_RPC_PASS` |                          | Password for `--rpc-login` (digest auth)     |
| `-listen`      | `DASHBOARD_LISTEN` | `127.0.0.1:8080`         | Address the dashboard listens on             |
| `-refresh`     |                    | `5s`                     | How often the page refreshes                 |
| `-rpc-timeout` |                    | `5s`                     | Timeout for each RPC request                 |

Prefer the environment variables for the password so it doesn't show up in `ps`.

Some fields (free space, uptime) are hidden by `monerod` on a restricted RPC port (`--restricted-rpc`); the dashboard shows them as `—`.

## HTTP endpoints

- `GET /`: the dashboard
- `GET /api/status`: JSON status (always `200`; `ok: false` and an `error` when the daemon is unreachable)
- `GET /healthz`: liveness check for the dashboard itself

## Development

```sh
go vet ./...
go test ./...
```

Layout:

```
cmd/monerod-dashboard/   entry point, flags
internal/rpc/            JSON-RPC client + digest auth
internal/status/         derived values and warnings
internal/server/         HTTP handlers and embedded web UI (web/)
```
