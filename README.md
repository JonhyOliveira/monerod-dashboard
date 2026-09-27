# monerod-dashboard

A dashboard for the monero daemon. Exposes some of the information available through the RPC interface.

A single Go binary (standard library only, plus a bundled copy of [htmx](https://htmx.org)) that talks to [`monerod`](https://github.com/monero-project/monero)'s RPC and serves a web interface to watch **and manage** your node:

- **Overview:** sync state, peers, chain, mempool, storage, uptime, traffic, fees, consensus, with warnings for common failures (unreachable, no peers, low disk, stale tip, update available)
- **Peers:** live connections, ban/unban hosts and subnets (searchable, paged ban list), connection limits, white/gray peer lists, public RPC nodes
- **Network:** bandwidth limits (set / reset), traffic totals and averages, sync peers and download spans
- **Mempool:** stats, age histogram, fee rates, transactions; relay or remove selected transactions, flush the pool
- **Blocks:** block list, block and transaction detail (decoded JSON, raw hex), search by height or hash, alternative chains
- **Mining:** status, start/stop, hash-rate logging, next-block data, block templates, submit blocks, generate blocks on regtest
- **Maintenance:** log level and categories, save the chain, flush caches, prune, pop blocks, check/download updates, stop the daemon
- **Tools:** broadcast raw transactions, key image status, output lookup, output histogram and distribution, emission sums, fee estimate, block hash by height, txid prefix search, PoW hash
- **RPC console:** send any JSON RPC request and see the raw response

Pages are rendered on the server. Live sections refresh every few seconds (the ring in the header shows when), and actions post in the background and report back in a toast. Everything also works without JavaScript, with full page loads.

Pages never wait on monerod. The dashboard keeps the latest answer to every read call in memory, and a background loop refreshes it: live data every `-refresh` (5s), slow-changing data (peer lists, consensus, fees) every `-slow-refresh` (1m). The overview's data is always kept warm; other pages' data stays warm while someone has viewed it in the last 10 minutes. Actions bypass the cache and clear it, so their effect shows immediately.

If monerod stops answering, the dashboard keeps showing the last data it fetched successfully. A badge in the header says how old it is (e.g. "data 2m 13s old", with the reason on hover), and the overview explains what's wrong. It appears when a page shows data more than two refresh intervals old (the reason is the latest refresh error, if any), and clears on its own once monerod answers again.

## Security

The dashboard can stop, prune and reconfigure your node, so **the whole dashboard sits behind a password login**:

- Set the password with `DASHBOARD_ADMIN_PASSWORD`, or `DASHBOARD_ADMIN_PASSWORD_FILE` for a Docker secret. The dashboard refuses to start without one, unless you pass `-insecure-no-auth`.
- Sessions use an `HttpOnly`, `SameSite=Strict` cookie (`Secure` behind HTTPS), expire after 12 hours idle, and every change is protected against cross-site request forgery.
- Repeated wrong passwords lock the client IP out for a growing amount of time.
- Destructive actions ask for a typed confirmation (`stop`, `prune`, `flush`, the number of blocks to pop), checked on the server.
- Pages send a strict Content-Security-Policy (no inline or third-party scripts).
- Every action is logged with the client IP.

The login is plain HTTP. If you reach the dashboard from outside your LAN, put it behind a TLS reverse proxy or a VPN.

## Connecting to monerod

Management needs monerod's **unrestricted** RPC. On a restricted port (`--restricted-rpc`) the dashboard still shows what it can and marks the rest as unavailable.

A good setup keeps a restricted port for wallets and a separate unrestricted port that only the dashboard can reach:

```
monerod --rpc-bind-ip 0.0.0.0 --rpc-bind-port 18083 \
        --rpc-restricted-bind-ip 0.0.0.0 --rpc-restricted-bind-port 18081 \
        --confirm-external-bind ...
```

Publish only 18081, and point the dashboard at 18083.

## Build & run

Requires Go 1.24+.

```sh
go build -o monerod-dashboard ./cmd/monerod-dashboard
DASHBOARD_ADMIN_PASSWORD='a long passphrase' ./monerod-dashboard -rpc-url http://127.0.0.1:18083
```

Then open http://127.0.0.1:8080.

## Docker

```sh
docker build -t monerod-dashboard .
docker run --rm -p 8080:8080 \
  -e MONEROD_RPC_URL=http://<node-host>:18083 \
  -e DASHBOARD_ADMIN_PASSWORD='a long passphrase' \
  monerod-dashboard
```

The image listens on `0.0.0.0:8080` inside the container. If monerod runs on the Docker host, use `--network host` with `-e MONEROD_RPC_URL=http://127.0.0.1:18083`, or on Docker Desktop point it at `http://host.docker.internal:18083`.

### Docker Compose

Alongside a `monerod` service on the same network:

```yaml
  monerod-dashboard:
    build:
      context: https://github.com/JonhyOliveira/monerod-dashboard.git#main
    image: monerod-dashboard:latest
    restart: unless-stopped
    environment:
      - MONEROD_RPC_URL=http://monerod:18083
      - DASHBOARD_ADMIN_PASSWORD=change-me   # or DASHBOARD_ADMIN_PASSWORD_FILE with a secret
    depends_on:
      - monerod
    ports:
      - "3000:8080"
```

## Configuration

| Flag                   | Env var                         | Default                  | Description                                          |
|------------------------|---------------------------------|--------------------------|------------------------------------------------------|
| `-rpc-url`             | `MONEROD_RPC_URL`               | `http://127.0.0.1:18081` | monerod RPC base URL                                 |
| `-rpc-user`            | `MONEROD_RPC_USER`              |                          | Username for monerod `--rpc-login` (digest auth)     |
| `-rpc-pass`            | `MONEROD_RPC_PASS`              |                          | Password for monerod `--rpc-login` (digest auth)     |
| `-admin-password`      | `DASHBOARD_ADMIN_PASSWORD`      |                          | Dashboard login password (required)                  |
| `-admin-password-file` | `DASHBOARD_ADMIN_PASSWORD_FILE` |                          | File holding the dashboard password                  |
| `-insecure-no-auth`    |                                 | off                      | Disable the login entirely                           |
| `-listen`              | `DASHBOARD_LISTEN`              | `127.0.0.1:8080`         | Address the dashboard listens on                     |
| `-refresh`             |                                 | `5s`                     | Refresh interval of live data and live sections      |
| `-slow-refresh`        |                                 | `1m`                     | Refresh interval of slow-changing data               |
| `-rpc-timeout`         |                                 | `30s`                    | Timeout for each RPC request                         |

Prefer environment variables or the password file over flags, so passwords don't show up in `ps`.

## RPC coverage

Every JSON method monerod offers has a place in the dashboard. Methods marked * need the unrestricted RPC port.

| Where           | RPC methods |
|-----------------|-------------|
| Overview        | `get_info`, `get_last_block_header`, `sync_info`*, `hard_fork_info`, `get_version`, `get_fee_estimate`, `get_net_stats`* |
| Peers           | `get_connections`*, `/get_peer_list`*, `/get_public_nodes`, `get_bans`*, `set_bans`*, `/out_peers`*, `/in_peers`* |
| Network         | `/get_limit`, `/set_limit`*, `/get_net_stats`*, `sync_info`* |
| Mempool         | `/get_transaction_pool`, `/get_transaction_pool_stats`, `get_txpool_backlog`, `flush_txpool`*, `relay_tx`* |
| Blocks          | `/get_height`, `get_block_headers_range`, `get_block`, `get_block_header_by_hash`, `get_alternate_chains`*, `/get_alt_blocks_hashes`* |
| Transaction     | `/get_transactions`, `relay_tx`*, `flush_txpool`* |
| Mining          | `/mining_status`*, `/start_mining`*, `/stop_mining`*, `/set_log_hash_rate`*, `get_miner_data`, `get_block_template`, `submit_block`, `generateblocks`* |
| Maintenance     | `/set_log_level`*, `/set_log_categories`*, `/save_bc`*, `flush_cache`*, `prune_blockchain`*, `/pop_blocks`*, `/update`*, `/stop_daemon`* |
| Tools           | `/send_raw_transaction`, `/is_key_image_spent`, `/get_outs`, `get_output_histogram`, `get_output_distribution`, `get_coinbase_tx_sum`*, `on_get_block_hash`, `get_txids_loose`, `calc_pow`* |
| RPC console     | anything else, e.g. `get_block_count`, `banned`*, `/get_transaction_pool_hashes`, `add_aux_pow` |

Not covered: the binary `.bin` endpoints (`get_blocks.bin` and friends). They exist for wallet synchronisation and each has a JSON equivalent above.

## Limits

Long lists are loaded in batches, not truncated: the mempool table, the peer lists and the ban list show their first 100 rows, and the next 100 load as you scroll to the end. Batches come from the same cached snapshot (monerod can't page these lists itself), so rows don't repeat or go missing while you scroll. While extra rows are shown, the live refresh pauses; click the ring to refresh.

Other deliberate limits:

| What | Limit | Why |
|------|-------|-----|
| Blocks list | 20 per page, with older/newer links | paging |
| Output distribution tool | shows the last 50 blocks (totals cover the whole range) | page size |
| Output lookup tool | 1,000 indices per query | request size |
| Generate blocks (regtest) | 1,000 per action | |
| Pop blocks | 100,000 per action | typo guard |
| Connection limits | outgoing ≤ 1,000, incoming ≤ 100,000 | typo guard |
| Mining threads | 1–256 | |
| Block template reserve size | 255 bytes | monerod's own maximum |
| Action form size | 1 MiB | |
| RPC response size | 1 GiB | memory guard |
| RPC timeout | 30s (`-rpc-timeout`) | |
| Login | locked out after 5 failures, backing off up to 15 min | brute force |
| Session | 12 h idle, 7 days max | |

## HTTP endpoints

- `GET /`, `/peers`, `/network`, `/mempool`, `/blocks`, `/block/{height|hash}`, `/tx/{hash}`, `/mining`, `/maintenance`, `/tools`, `/console`: the dashboard
- `GET /mempool/rows`, `/peers/rows`: the next batch of a long table (htmx)
- `POST /actions/{name}`: management actions (session and CSRF token required)
- `GET /api/status`: JSON status (session required; `ok: false` and an `error` when there is no data at all, `stale: true` and `data_as_of` when showing last known data)
- `GET /healthz`: liveness check for the dashboard itself (public)

## Development

```sh
go vet ./...
go test ./...
```

Tests run against a fake monerod that replays responses recorded from a real one (`internal/rpc/testdata`).

To try everything against a real node, `scripts/dev-regtest.sh` starts a local two-node regtest network and a funded wallet (needs `monerod` and `monero-wallet-rpc` on `PATH`):

```sh
scripts/dev-regtest.sh start      # node RPC :18083 (restricted :18081)
scripts/dev-regtest.sh tx         # put a transaction in the mempool
DASHBOARD_ADMIN_PASSWORD=dev go run ./cmd/monerod-dashboard -rpc-url http://127.0.0.1:18083
scripts/dev-regtest.sh stop
```

Layout:

```
cmd/monerod-dashboard/     entry point, flags
internal/rpc/              typed monerod RPC client, digest auth, cache
internal/rpc/rpctest/      fake monerod for tests
internal/status/           overview status and warnings
internal/server/           handlers, auth, actions
internal/server/templates/ page templates (html/template)
internal/server/web/       CSS, app.js, vendored htmx
scripts/dev-regtest.sh     local regtest network for development
```
