package rpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// PoolTx is a transaction in the pool (/get_transaction_pool).
type PoolTx struct {
	IDHash             string `json:"id_hash"`
	BlobSize           uint64 `json:"blob_size"`
	Weight             uint64 `json:"weight"`
	Fee                uint64 `json:"fee"`
	ReceiveTime        int64  `json:"receive_time"`
	LastRelayedTime    int64  `json:"last_relayed_time"`
	Relayed            bool   `json:"relayed"`
	DoNotRelay         bool   `json:"do_not_relay"`
	DoubleSpendSeen    bool   `json:"double_spend_seen"`
	KeptByBlock        bool   `json:"kept_by_block"`
	LastFailedHeight   uint64 `json:"last_failed_height"`
	MaxUsedBlockHeight uint64 `json:"max_used_block_height"`
	TxJSON             string `json:"tx_json"`
}

// TransactionPool is /get_transaction_pool.
type TransactionPool struct {
	Status
	Transactions   []PoolTx `json:"transactions"`
	SpentKeyImages []struct {
		IDHash    string   `json:"id_hash"`
		TxsHashes []string `json:"txs_hashes"`
	} `json:"spent_key_images"`
}

// GetTransactionPool calls /get_transaction_pool.
func (c *Client) GetTransactionPool(ctx context.Context) (*TransactionPool, error) {
	var r TransactionPool
	return &r, c.CallPath(ctx, "/get_transaction_pool", nil, &r)
}

// GetTransactionPoolHashes calls /get_transaction_pool_hashes.
func (c *Client) GetTransactionPoolHashes(ctx context.Context) ([]string, error) {
	var r struct {
		Status
		TxHashes []string `json:"tx_hashes"`
	}
	return r.TxHashes, c.CallPath(ctx, "/get_transaction_pool_hashes", nil, &r)
}

// PoolStats is the pool_stats of /get_transaction_pool_stats.
type PoolStats struct {
	BytesMax        uint32 `json:"bytes_max"`
	BytesMed        uint32 `json:"bytes_med"`
	BytesMin        uint32 `json:"bytes_min"`
	BytesTotal      uint64 `json:"bytes_total"`
	FeeTotal        uint64 `json:"fee_total"`
	Histo98pc       int64  `json:"histo_98pc"`
	Num10m          uint32 `json:"num_10m"`
	NumDoubleSpends uint32 `json:"num_double_spends"`
	NumFailing      uint32 `json:"num_failing"`
	NumNotRelayed   uint32 `json:"num_not_relayed"`
	Oldest          int64  `json:"oldest"`
	TxsTotal        uint32 `json:"txs_total"`
	Histo           []struct {
		Bytes uint32 `json:"bytes"`
		Txs   uint32 `json:"txs"`
	} `json:"histo"`
}

// GetTransactionPoolStats calls /get_transaction_pool_stats.
func (c *Client) GetTransactionPoolStats(ctx context.Context) (*PoolStats, error) {
	var r struct {
		Status
		PoolStats PoolStats `json:"pool_stats"`
	}
	return &r.PoolStats, c.CallPath(ctx, "/get_transaction_pool_stats", nil, &r)
}

// BacklogEntry is one transaction in get_txpool_backlog.
type BacklogEntry struct {
	Weight     uint64
	Fee        uint64
	TimeInPool int64
}

// GetTxpoolBacklog calls get_txpool_backlog. monerod returns the backlog as
// raw packed structs inside a JSON string, which encoding/json would mangle
// (the bytes are not UTF-8), so the string is decoded by hand.
func (c *Client) GetTxpoolBacklog(ctx context.Context) ([]BacklogEntry, error) {
	raw, err := c.Raw(ctx, "/json_rpc", "get_txpool_backlog", nil)
	if err != nil {
		return nil, err
	}
	blob, found, err := rawJSONString(raw, "backlog")
	if err != nil {
		return nil, fmt.Errorf("get_txpool_backlog: %w", err)
	}
	if !found {
		if bytes.Contains(raw, []byte(`"error"`)) {
			return nil, fmt.Errorf("get_txpool_backlog: %w", ErrUnsupported)
		}
		return nil, nil // empty pool: monerod omits the field
	}
	const size = 24
	if len(blob)%size != 0 {
		return nil, fmt.Errorf("get_txpool_backlog: unexpected backlog length %d", len(blob))
	}
	out := make([]BacklogEntry, 0, len(blob)/size)
	for i := 0; i < len(blob); i += size {
		out = append(out, BacklogEntry{
			Weight:     binary.LittleEndian.Uint64(blob[i:]),
			Fee:        binary.LittleEndian.Uint64(blob[i+8:]),
			TimeInPool: int64(binary.LittleEndian.Uint64(blob[i+16:])),
		})
	}
	return out, nil
}

// rawJSONString finds `"key": "..."` in raw JSON and returns the string's
// bytes with JSON escapes resolved, keeping non-UTF-8 bytes intact.
func rawJSONString(raw []byte, key string) ([]byte, bool, error) {
	needle := []byte(`"` + key + `"`)
	i := bytes.Index(raw, needle)
	if i < 0 {
		return nil, false, nil
	}
	rest := bytes.TrimLeft(raw[i+len(needle):], " \t\r\n")
	if len(rest) == 0 || rest[0] != ':' {
		return nil, false, errors.New("malformed JSON")
	}
	rest = bytes.TrimLeft(rest[1:], " \t\r\n")
	if len(rest) == 0 || rest[0] != '"' {
		return nil, false, errors.New("value is not a string")
	}
	var out []byte
	for j := 1; j < len(rest); j++ {
		b := rest[j]
		switch {
		case b == '"':
			return out, true, nil
		case b != '\\':
			out = append(out, b)
			continue
		}
		j++
		if j >= len(rest) {
			break
		}
		switch rest[j] {
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			if j+4 >= len(rest) {
				return nil, false, errors.New("truncated \\u escape")
			}
			v, err := strconv.ParseUint(string(rest[j+1:j+5]), 16, 32)
			if err != nil {
				return nil, false, err
			}
			if v < 0x100 {
				out = append(out, byte(v))
			} else {
				out = utf8.AppendRune(out, rune(v))
			}
			j += 4
		default: // \" \\ \/
			out = append(out, rest[j])
		}
	}
	return nil, false, errors.New("unterminated string")
}

// FlushTxpool calls flush_txpool. With no txids the whole pool is flushed.
func (c *Client) FlushTxpool(ctx context.Context, txids []string) error {
	var r Status
	return c.Call(ctx, "flush_txpool", map[string][]string{"txids": nonNil(txids)}, &r)
}

// RelayTx calls relay_tx for transactions in the pool.
func (c *Client) RelayTx(ctx context.Context, txids []string) error {
	var r Status
	return c.Call(ctx, "relay_tx", map[string][]string{"txids": nonNil(txids)}, &r)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// IsHash reports whether s looks like a 32-byte hex hash.
func IsHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
