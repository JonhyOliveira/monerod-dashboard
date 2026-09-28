package xmr

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Address is a decoded Monero address.
type Address struct {
	Network string // mainnet, testnet or stagenet
	Kind    string // standard, integrated or subaddress
	Spend   []byte // public spend key
	View    []byte // public view key
	// PaymentID is an integrated address's 8-byte payment ID.
	PaymentID []byte
}

// Subaddress reports whether the address is a subaddress, whose
// transactions use a different public key form.
func (a *Address) Subaddress() bool { return a.Kind == "subaddress" }

var prefixes = map[uint64][2]string{
	18: {"mainnet", "standard"}, 19: {"mainnet", "integrated"}, 42: {"mainnet", "subaddress"},
	53: {"testnet", "standard"}, 54: {"testnet", "integrated"}, 63: {"testnet", "subaddress"},
	24: {"stagenet", "standard"}, 25: {"stagenet", "integrated"}, 36: {"stagenet", "subaddress"},
}

// ParseAddress decodes and checksums a Monero address.
func ParseAddress(s string) (*Address, error) {
	raw, err := base58Decode(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	if len(raw) < 4 {
		return nil, errors.New("address is too short")
	}
	body, sum := raw[:len(raw)-4], raw[len(raw)-4:]
	if want := Keccak256(body); string(want[:4]) != string(sum) {
		return nil, errors.New("address checksum does not match (typo?)")
	}
	tag, n := readVarint(body)
	kind, ok := prefixes[tag]
	if n == 0 || !ok {
		return nil, fmt.Errorf("unknown address prefix %d", tag)
	}
	keys := body[n:]
	want := 64
	if kind[1] == "integrated" {
		want = 72
	}
	if len(keys) != want {
		return nil, fmt.Errorf("%s address has the wrong length", kind[1])
	}
	a := &Address{Network: kind[0], Kind: kind[1], Spend: keys[:32], View: keys[32:64]}
	if want == 72 {
		a.PaymentID = keys[64:]
	}
	if _, err := point(a.Spend); err != nil {
		return nil, errors.New("address has an invalid spend key")
	}
	if _, err := point(a.View); err != nil {
		return nil, errors.New("address has an invalid view key")
	}
	return a, nil
}

// Monero's base58 encodes 8-byte blocks as 11 characters each; a final
// partial block of n bytes takes blockSizes[n] characters.
const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var blockSizes = []int{0, 2, 3, 5, 6, 7, 9, 10, 11}

func base58Decode(s string) ([]byte, error) {
	var out []byte
	for len(s) > 0 {
		chunk := s[:min(11, len(s))]
		s = s[len(chunk):]
		n := -1
		for i, size := range blockSizes {
			if size == len(chunk) {
				n = i
			}
		}
		if n < 0 {
			return nil, errors.New("not a valid address (bad length)")
		}
		v := new(big.Int)
		for _, c := range chunk {
			d := strings.IndexRune(alphabet, c)
			if d < 0 {
				return nil, fmt.Errorf("not a valid address (%q is not a base58 character)", c)
			}
			v.Mul(v, big.NewInt(58)).Add(v, big.NewInt(int64(d)))
		}
		if v.BitLen() > 8*n {
			return nil, errors.New("not a valid address (bad block)")
		}
		block := make([]byte, n)
		v.FillBytes(block)
		out = append(out, block...)
	}
	return out, nil
}
