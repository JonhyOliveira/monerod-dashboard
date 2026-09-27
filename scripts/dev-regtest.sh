#!/usr/bin/env bash
# Starts a local two-node Monero regtest network plus a funded wallet, for
# developing and testing the dashboard against a real monerod.
#
#   node A: unrestricted RPC 127.0.0.1:18083, restricted RPC 127.0.0.1:18081
#   node B: RPC 127.0.0.1:18093 (peer of A, so A has connections)
#   wallet: monero-wallet-rpc on 127.0.0.1:18088, mined to and able to send
#
# Usage: scripts/dev-regtest.sh [start|stop|tx]
#   start  start everything (default)
#   tx     send a transaction so the mempool has something in it
#   stop   stop everything
set -euo pipefail

DIR=${REGTEST_DIR:-/tmp/monerod-dashboard-regtest}
A_RPC=18083 A_RESTRICTED=18081 A_P2P=38080
B_RPC=18093 B_P2P=38090
WALLET_RPC=18088

rpc() { # port method params; fails on HTTP or JSON-RPC errors
  local out
  out=$(curl -sf "http://127.0.0.1:$1/json_rpc" -d "{\"jsonrpc\":\"2.0\",\"id\":\"0\",\"method\":\"$2\",\"params\":${3:-{\}}}") || return 1
  if grep -q '"error"' <<<"$out"; then echo "$out" >&2; return 1; fi
  echo "$out"
}
wait_for() { for _ in $(seq 1 60); do curl -sf "http://127.0.0.1:$1/get_info" >/dev/null 2>&1 && return; sleep 1; done; echo "port $1 did not come up" >&2; exit 1; }

stop() {
  pkill -f "monero-wallet-rpc.*--rpc-bind-port $WALLET_RPC" 2>/dev/null || true
  pkill -f "monerod.*--data-dir $DIR/" 2>/dev/null || true
  local pattern="monerod.*--data-dir $DIR/|monero-wallet-rpc.*--rpc-bind-port $WALLET_RPC"
  for _ in $(seq 1 30); do
    pgrep -f "$pattern" >/dev/null || return 0
    sleep 1
  done
  # Still running after a graceful shutdown window: force it.
  pkill -9 -f "$pattern" 2>/dev/null || true
  sleep 1
}

address() { rpc $WALLET_RPC get_address | python3 -c 'import json,sys; print(json.load(sys.stdin)["result"]["address"])'; }

# Daemons run under setsid so they outlive the shell that started them.
node() { # name p2p-port rpc-port peer-p2p-port [extra args...]
  local name=$1 p2p=$2 rpcport=$3 peer=$4; shift 4
  setsid monerod --regtest --no-zmq --fixed-difficulty 1 --non-interactive --no-igd --allow-local-ip \
    --log-level 0 --data-dir "$DIR/$name" --p2p-bind-ip 127.0.0.1 --p2p-bind-port "$p2p" \
    --rpc-bind-ip 127.0.0.1 --rpc-bind-port "$rpcport" --add-exclusive-node "127.0.0.1:$peer" \
    "$@" > "$DIR/$name.log" 2>&1 < /dev/null &
  wait_for "$rpcport"
}

start() {
  stop
  for port in $A_RPC $A_RESTRICTED $B_RPC $WALLET_RPC; do
    if curl -s "http://127.0.0.1:$port/" >/dev/null 2>&1; then
      echo "port $port is already in use; stop whatever is listening there first" >&2; exit 1
    fi
  done
  mkdir -p "$DIR"

  node a $A_P2P $A_RPC $B_P2P --rpc-restricted-bind-ip 127.0.0.1 --rpc-restricted-bind-port $A_RESTRICTED
  node b $B_P2P $B_RPC $A_P2P

  # The wallet must start after the node it syncs from.
  mkdir -p "$DIR/wallets"
  setsid monero-wallet-rpc --allow-mismatched-daemon-version --daemon-address 127.0.0.1:$A_RPC --trusted-daemon \
    --rpc-bind-ip 127.0.0.1 --rpc-bind-port $WALLET_RPC --disable-rpc-login \
    --wallet-dir "$DIR/wallets" --log-file "$DIR/wallet-rpc.log" --log-level 0 > "$DIR/wallet.log" 2>&1 < /dev/null &
  for _ in $(seq 1 60); do rpc $WALLET_RPC get_version >/dev/null 2>&1 && break; sleep 1; done
  rpc $WALLET_RPC open_wallet '{"filename":"dev"}' >/dev/null 2>&1 \
    || rpc $WALLET_RPC create_wallet '{"filename":"dev","language":"English"}' >/dev/null

  # Mine enough blocks for coinbase outputs to unlock (60), plus spare.
  # generateblocks is refused for a while after startup, so retry.
  local addr; addr=$(address)
  for _ in $(seq 1 60); do
    local h; h=$(rpc $A_RPC get_block_count | python3 -c 'import json,sys; print(json.load(sys.stdin)["result"]["count"])')
    [ "$h" -ge 130 ] && break
    rpc $A_RPC generateblocks "{\"amount_of_blocks\":10,\"wallet_address\":\"$addr\"}" >/dev/null 2>&1 || sleep 1
  done
  rpc $WALLET_RPC refresh >/dev/null
  echo "regtest up: node A rpc :$A_RPC (restricted :$A_RESTRICTED), node B :$B_RPC, wallet :$WALLET_RPC"
}

tx() {
  rpc $WALLET_RPC refresh >/dev/null
  rpc $WALLET_RPC transfer "{\"destinations\":[{\"amount\":1000000000,\"address\":\"$(address)\"}],\"priority\":1}" \
    | python3 -c 'import json,sys; r=json.load(sys.stdin); print(r["result"]["tx_hash"] if "result" in r else r)'
}

case "${1:-start}" in
  start) start ;;
  stop) stop ;;
  tx) tx ;;
  *) echo "usage: $0 [start|stop|tx]" >&2; exit 2 ;;
esac
