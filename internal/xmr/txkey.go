package xmr

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"filippo.io/edwards25519"
)

// Received is an output of a transaction that pays the checked address.
type Received struct {
	Index int
	Key   string // the output's one-time public key
	// Amount is in atomic units; AmountKnown is false for the old RingCT
	// formats (before October 2018), whose amounts this doesn't decode.
	Amount      uint64
	AmountKnown bool
}

// Proof is the result of checking a transaction key against an address,
// like monero-wallet-rpc's check_tx_key.
type Proof struct {
	Outputs []Received
	Total   uint64 // sum of the known amounts
	// KeyMatches is whether the key's public key is the transaction's (for
	// this address: a payment to a single subaddress derives it from that
	// subaddress), so the key provably belongs to this transaction.
	KeyMatches bool
	// PaymentID is the decrypted payment ID of an integrated-address
	// payment, in hex.
	PaymentID string
}

type txJSON struct {
	Version int `json:"version"`
	Vout    []struct {
		Amount uint64 `json:"amount"`
		Target struct {
			Key       string `json:"key"`
			TaggedKey *struct {
				Key string `json:"key"`
			} `json:"tagged_key"`
		} `json:"target"`
	} `json:"vout"`
	Extra []int `json:"extra"`
	RCT   *struct {
		Type     int `json:"type"`
		EcdhInfo []struct {
			Amount string `json:"amount"`
		} `json:"ecdhInfo"`
		OutPk []string `json:"outPk"`
	} `json:"rct_signatures"`
}

// ParseTxKey parses a transaction private key as monero-wallet-rpc's
// get_tx_key prints it: the main key, then one per output for
// transactions that need them, as one hex string.
func ParseTxKey(s string) (main *edwards25519.Scalar, additional []*edwards25519.Scalar, err error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s)%64 != 0 {
		return nil, nil, errors.New("the transaction key must be 64 hex characters (or a multiple, with per-output keys)")
	}
	for i := 0; i < len(s); i += 64 {
		b, err := hex32(s[i : i+64])
		if err != nil {
			return nil, nil, errors.New("the transaction key must be hex")
		}
		k, err := edwards25519.NewScalar().SetCanonicalBytes(b)
		if err != nil {
			return nil, nil, errors.New("the transaction key is not a valid private key")
		}
		if i == 0 {
			main = k
		} else {
			additional = append(additional, k)
		}
	}
	return main, additional, nil
}

// CheckTxKey finds the outputs of a transaction (monerod's decoded JSON)
// that pay addr, using the sender's transaction key.
func CheckTxKey(asJSON, txKey string, addr *Address) (*Proof, error) {
	var tx txJSON
	if err := json.Unmarshal([]byte(asJSON), &tx); err != nil {
		return nil, fmt.Errorf("decoding the transaction: %w", err)
	}
	r, rs, err := ParseTxKey(txKey)
	if err != nil {
		return nil, err
	}
	viewKey, _ := point(addr.View)
	spendKey, _ := point(addr.Spend)

	// The derivation D = 8·r·A is the secret shared between sender and
	// recipient; per-output keys give per-output derivations.
	derive := func(k *edwards25519.Scalar) []byte {
		p := new(edwards25519.Point).ScalarMult(k, viewKey)
		return p.MultByCofactor(p).Bytes()
	}
	main := derive(r)
	var extraDerivs [][]byte
	for _, k := range rs {
		extraDerivs = append(extraDerivs, derive(k))
	}

	proof := &Proof{}
	txPub, extraPubs, nonce := parseExtra(tx.Extra)
	proof.KeyMatches = matchesPub(r, spendKey, txPub)
	for i, k := range rs {
		if i < len(extraPubs) && matchesPub(k, spendKey, extraPubs[i]) {
			proof.KeyMatches = true
		}
	}
	// An integrated address's payment ID is encrypted with the main
	// derivation.
	if len(nonce) == 9 && nonce[0] == 0x01 {
		mask := Keccak256(main, []byte{0x8d})
		pid := make([]byte, 8)
		for i := range pid {
			pid[i] = nonce[1+i] ^ mask[i]
		}
		proof.PaymentID = hex.EncodeToString(pid)
	}

	for i, out := range tx.Vout {
		key := out.Target.Key
		if out.Target.TaggedKey != nil {
			key = out.Target.TaggedKey.Key
		}
		derivs := [][]byte{main}
		if i < len(extraDerivs) {
			derivs = append(derivs, extraDerivs[i])
		}
		for _, d := range derivs {
			// The output is ours if its key is Hs(D || i)·G + B.
			s := hashToScalar(d, varint(uint64(i)))
			want := new(edwards25519.Point).ScalarBaseMult(s)
			want.Add(want, spendKey)
			if hex.EncodeToString(want.Bytes()) != key {
				continue
			}
			rec := Received{Index: i, Key: key}
			if rec.Amount, rec.AmountKnown, err = decodeAmount(&tx, i, s); err != nil {
				return nil, err
			}
			proof.Outputs = append(proof.Outputs, rec)
			proof.Total += rec.Amount
			break
		}
	}
	return proof, nil
}

// matchesPub reports whether pub is k·G (a standard address's transaction
// key) or k·B (a subaddress's).
func matchesPub(k *edwards25519.Scalar, spend *edwards25519.Point, pub []byte) bool {
	if pub == nil {
		return false
	}
	if string(new(edwards25519.Point).ScalarBaseMult(k).Bytes()) == string(pub) {
		return true
	}
	return string(new(edwards25519.Point).ScalarMult(k, spend).Bytes()) == string(pub)
}

// decodeAmount decrypts output i's amount with its shared secret s and
// checks it against the output's Pedersen commitment.
func decodeAmount(tx *txJSON, i int, s *edwards25519.Scalar) (uint64, bool, error) {
	if tx.RCT == nil || tx.RCT.Type == 0 { // amounts in the clear
		return tx.Vout[i].Amount, true, nil
	}
	if tx.RCT.Type < 4 || i >= len(tx.RCT.EcdhInfo) || i >= len(tx.RCT.OutPk) {
		return 0, false, nil
	}
	enc, err := hex.DecodeString(tx.RCT.EcdhInfo[i].Amount)
	if err != nil || len(enc) != 8 {
		return 0, false, fmt.Errorf("output %d has a malformed encrypted amount", i)
	}
	sb := s.Bytes()
	pad := Keccak256([]byte("amount"), sb)
	var plain [8]byte
	for j := range plain {
		plain[j] = enc[j] ^ pad[j]
	}
	amount := binary.LittleEndian.Uint64(plain[:])

	// The commitment is mask·G + amount·H; a matching one proves the amount.
	mask := hashToScalar([]byte("commitment_mask"), sb)
	var ab [32]byte
	binary.LittleEndian.PutUint64(ab[:], amount)
	a, _ := edwards25519.NewScalar().SetCanonicalBytes(ab[:])
	c := new(edwards25519.Point).VarTimeMultiScalarMult([]*edwards25519.Scalar{mask, a}, []*edwards25519.Point{edwards25519.NewGeneratorPoint(), h})
	if hex.EncodeToString(c.Bytes()) != tx.RCT.OutPk[i] {
		return 0, false, fmt.Errorf("output %d's decrypted amount does not match its commitment", i)
	}
	return amount, true, nil
}

// parseExtra reads the transaction public key, the per-output public keys
// and the nonce (which carries payment IDs) from tx_extra.
func parseExtra(extra []int) (pub []byte, pubs [][]byte, nonce []byte) {
	b := make([]byte, len(extra))
	for i, v := range extra {
		b[i] = byte(v)
	}
	for len(b) > 0 {
		tag := b[0]
		b = b[1:]
		switch tag {
		case 0x00: // padding runs to the end
			return
		case 0x01:
			if len(b) < 32 {
				return
			}
			if pub == nil {
				pub = b[:32]
			}
			b = b[32:]
		case 0x04:
			n, l := readVarint(b)
			if l == 0 || uint64(len(b)-l) < 32*n {
				return
			}
			b = b[l:]
			for j := uint64(0); j < n; j++ {
				pubs = append(pubs, b[:32])
				b = b[32:]
			}
		case 0x02, 0x03, 0xde: // nonce, merge mining, minergate: length-prefixed
			n, l := readVarint(b)
			if l == 0 || uint64(len(b)-l) < n {
				return
			}
			if tag == 0x02 && nonce == nil {
				nonce = b[l : l+int(n)]
			}
			b = b[l+int(n):]
		default:
			return
		}
	}
	return
}
