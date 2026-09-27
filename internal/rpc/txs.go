package rpc

import "context"

// Transaction is an entry of /get_transactions.
type Transaction struct {
	TxHash            string   `json:"tx_hash"`
	AsHex             string   `json:"as_hex"`
	AsJSON            string   `json:"as_json"`
	InPool            bool     `json:"in_pool"`
	DoubleSpendSeen   bool     `json:"double_spend_seen"`
	BlockHeight       uint64   `json:"block_height"`
	BlockTimestamp    int64    `json:"block_timestamp"`
	Confirmations     uint64   `json:"confirmations"`
	ReceivedTimestamp int64    `json:"received_timestamp"`
	Relayed           bool     `json:"relayed"`
	OutputIndices     []uint64 `json:"output_indices"`
	PrunableHash      string   `json:"prunable_hash"`
}

// GetTransactions calls /get_transactions with decode_as_json. Hashes the
// daemon does not know are returned in missed.
func (c *Client) GetTransactions(ctx context.Context, hashes []string) (txs []Transaction, missed []string, err error) {
	var r struct {
		Status
		Txs      []Transaction `json:"txs"`
		MissedTx []string      `json:"missed_tx"`
	}
	err = c.CallPath(ctx, "/get_transactions", map[string]any{"txs_hashes": nonNil(hashes), "decode_as_json": true}, &r)
	return r.Txs, r.MissedTx, err
}

// SendRawTxResult is /send_raw_transaction. Reason and the flags explain a
// rejection.
type SendRawTxResult struct {
	Status
	Reason            string `json:"reason"`
	NotRelayed        bool   `json:"not_relayed"`
	DoubleSpend       bool   `json:"double_spend"`
	FeeTooLow         bool   `json:"fee_too_low"`
	InvalidInput      bool   `json:"invalid_input"`
	InvalidOutput     bool   `json:"invalid_output"`
	LowMixin          bool   `json:"low_mixin"`
	Overspend         bool   `json:"overspend"`
	SanityCheckFailed bool   `json:"sanity_check_failed"`
	TooBig            bool   `json:"too_big"`
	TooFewOutputs     bool   `json:"too_few_outputs"`
	TxExtraTooBig     bool   `json:"tx_extra_too_big"`
	NonzeroUnlockTime bool   `json:"nonzero_unlock_time"`
}

// SendRawTransaction calls /send_raw_transaction. A rejected transaction
// returns the result together with a *StatusError.
func (c *Client) SendRawTransaction(ctx context.Context, txHex string, doNotRelay, sanityChecks bool) (*SendRawTxResult, error) {
	var r SendRawTxResult
	err := c.CallPath(ctx, "/send_raw_transaction", map[string]any{
		"tx_as_hex": txHex, "do_not_relay": doNotRelay, "do_sanity_checks": sanityChecks,
	}, &r)
	return &r, err
}

// Key image spent states from /is_key_image_spent.
const (
	KeyImageUnspent      = 0
	KeyImageSpentInChain = 1
	KeyImageSpentInPool  = 2
)

// IsKeyImageSpent calls /is_key_image_spent.
func (c *Client) IsKeyImageSpent(ctx context.Context, keyImages []string) ([]int, error) {
	var r struct {
		Status
		SpentStatus []int `json:"spent_status"`
	}
	return r.SpentStatus, c.CallPath(ctx, "/is_key_image_spent", map[string][]string{"key_images": nonNil(keyImages)}, &r)
}

// OutRef identifies an output by amount (0 for RingCT) and global index.
type OutRef struct {
	Amount uint64 `json:"amount"`
	Index  uint64 `json:"index"`
}

// Out is an entry of /get_outs.
type Out struct {
	Height   uint64 `json:"height"`
	Key      string `json:"key"`
	Mask     string `json:"mask"`
	TxID     string `json:"txid"`
	Unlocked bool   `json:"unlocked"`
}

// GetOuts calls /get_outs.
func (c *Client) GetOuts(ctx context.Context, outs []OutRef) ([]Out, error) {
	var r struct {
		Status
		Outs []Out `json:"outs"`
	}
	return r.Outs, c.CallPath(ctx, "/get_outs", map[string]any{"outputs": outs, "get_txid": true}, &r)
}

// HistogramEntry is an entry of get_output_histogram.
type HistogramEntry struct {
	Amount            uint64 `json:"amount"`
	TotalInstances    uint64 `json:"total_instances"`
	UnlockedInstances uint64 `json:"unlocked_instances"`
	RecentInstances   uint64 `json:"recent_instances"`
}

// HistogramParams are get_output_histogram's parameters.
type HistogramParams struct {
	Amounts      []uint64 `json:"amounts"`
	MinCount     uint64   `json:"min_count"`
	MaxCount     uint64   `json:"max_count"`
	Unlocked     bool     `json:"unlocked"`
	RecentCutoff uint64   `json:"recent_cutoff"`
}

// GetOutputHistogram calls get_output_histogram.
func (c *Client) GetOutputHistogram(ctx context.Context, p HistogramParams) ([]HistogramEntry, error) {
	if p.Amounts == nil {
		p.Amounts = []uint64{}
	}
	var r struct {
		Status
		Histogram []HistogramEntry `json:"histogram"`
	}
	return r.Histogram, c.Call(ctx, "get_output_histogram", p, &r)
}

// Distribution is an entry of get_output_distribution: the number of
// outputs of Amount created per block, starting at StartHeight.
type Distribution struct {
	Amount       uint64   `json:"amount"`
	Base         uint64   `json:"base"`
	StartHeight  uint64   `json:"start_height"`
	Distribution []uint64 `json:"distribution"`
}

// GetOutputDistribution calls get_output_distribution in its JSON (not
// binary) form.
func (c *Client) GetOutputDistribution(ctx context.Context, amounts []uint64, from, to uint64, cumulative bool) ([]Distribution, error) {
	if amounts == nil {
		amounts = []uint64{0}
	}
	var r struct {
		Status
		Distributions []Distribution `json:"distributions"`
	}
	return r.Distributions, c.Call(ctx, "get_output_distribution", map[string]any{
		"amounts": amounts, "from_height": from, "to_height": to,
		"cumulative": cumulative, "binary": false, "compress": false,
	}, &r)
}

// GetTxidsLoose calls get_txids_loose: txids whose first numBits bits match
// the hex template (monerod v0.18.4+).
func (c *Client) GetTxidsLoose(ctx context.Context, template string, numBits uint32) ([]string, error) {
	var r struct {
		Status
		TxIDs []string `json:"txids"`
	}
	return r.TxIDs, c.Call(ctx, "get_txids_loose", map[string]any{"txid_template": template, "num_matching_bits": numBits}, &r)
}
