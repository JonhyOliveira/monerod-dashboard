// Package xmr checks Monero payment proofs: given a transaction's private
// key and a recipient address, which outputs pay that address and how
// much. Curve arithmetic is filippo.io/edwards25519; hashing is the
// original Keccak-256 from golang.org/x/crypto/sha3.
package xmr

import (
	"encoding/hex"
	"fmt"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/sha3"
)

// Keccak256 is Monero's cn_fast_hash of the concatenated arguments.
func Keccak256(data ...[]byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	for _, d := range data {
		h.Write(d)
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// hashToScalar is Monero's hash_to_scalar: Keccak-256 reduced mod l.
func hashToScalar(data ...[]byte) *edwards25519.Scalar {
	h := Keccak256(data...)
	var wide [64]byte
	copy(wide[:], h[:])
	s, _ := edwards25519.NewScalar().SetUniformBytes(wide[:])
	return s
}

// varint is Monero's unsigned varint encoding (LEB128).
func varint(n uint64) []byte {
	var out []byte
	for n >= 0x80 {
		out = append(out, byte(n)|0x80)
		n >>= 7
	}
	return append(out, byte(n))
}

// readVarint decodes a varint at the start of b, returning the value and
// its length (0 if b ends first or it overflows).
func readVarint(b []byte) (uint64, int) {
	var n uint64
	for i, c := range b {
		if i == 10 {
			break
		}
		n |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return n, i + 1
		}
	}
	return 0, 0
}

func hex32(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("%q is not 64 hex characters", s)
	}
	return b, nil
}

func point(b []byte) (*edwards25519.Point, error) {
	p, err := new(edwards25519.Point).SetBytes(b)
	if err != nil {
		return nil, fmt.Errorf("%x is not a valid public key", b)
	}
	return p, nil
}

// h is RingCT's second generator, which amounts are committed to.
var h, _ = point(mustHex("8b655970153799af2aeadc9ff1add0ea6c7251d54154cfa92c173a0dd39c1f94"))

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
