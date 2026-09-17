package crypto

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const tronVersion byte = 0x41

// tronAddressLen is the fixed length of a Tron base58check address. The 21-byte
// payload (0x41 || 20-byte address) plus 4-byte checksum always encodes to 34
// base58 characters because the leading 0x41 keeps the value above 58^33.
const tronAddressLen = 34

// tronAddrSpace is 2^(8*AddressSize) = the exclusive upper bound of the 20-byte
// EVM address integer space.
var tronAddrSpace = new(big.Int).Lsh(big.NewInt(1), 8*AddressSize)

// TronPrefixAddressRange returns the inclusive range [lo, hi] of 20-byte EVM
// addresses whose Tron base58check address begins with prefix (which must start
// with 'T'). base58check(0x41||addr) is strictly increasing in addr —
// incrementing addr adds 2^32 to the encoded integer while the 4-byte checksum
// can only drop by at most 2^32-1, so the value still rises — so the matching
// addresses form one contiguous interval, found by binary search. ok is false
// when no Tron address can carry the prefix (it sorts outside [min, max]).
//
// This lets the CUDA backend match a Tron vanity prefix with a 160-bit integer
// range compare per candidate instead of a full base58 encode (see
// PROVANITY_CUDA_MODE_TRON_RANGE), with the exact base58check recomputed here on
// the CPU only for the rare candidate that lands in range.
func TronPrefixAddressRange(prefix string) (lo, hi [AddressSize]byte, ok bool) {
	m := len(prefix)
	if m == 0 || m > tronAddressLen {
		return lo, hi, false
	}
	cmpPrefix := func(x *big.Int) int {
		addr := bigToAddress(x)
		s, err := TronAddressFromEVMAddress(addr[:])
		if err != nil {
			panic(err) // addr is always AddressSize bytes, so this cannot fail
		}
		return strings.Compare(s[:m], prefix)
	}
	loAddr := tronLowerBound(func(x *big.Int) bool { return cmpPrefix(x) >= 0 })
	if loAddr.Cmp(tronAddrSpace) == 0 || cmpPrefix(loAddr) != 0 {
		return lo, hi, false
	}
	hiExcl := tronLowerBound(func(x *big.Int) bool { return cmpPrefix(x) > 0 })
	hiAddr := new(big.Int).Sub(hiExcl, big.NewInt(1))
	return bigToAddress(loAddr), bigToAddress(hiAddr), true
}

// TronPrefixLadder returns nested [lo, hi] address intervals, one per prefix
// length. prefix must be the implicit leading 'T' followed by the concrete
// prefix characters (e.g. "TABC"); level j (0-based) is the interval of 20-byte
// addresses whose Tron base58check address shares the first j+1 characters
// after 'T' — i.e. matches prefix[:j+2]. Because base58check is strictly
// increasing in the address, level j+1 ⊆ level j, so the CUDA scorer can report
// "matched character count" as the depth of the deepest interval that contains
// a candidate. los[j]/his[j] therefore have ascending lo and descending hi. ok
// is false if prefix is malformed or any level is unreachable.
func TronPrefixLadder(prefix string) (los, his [][AddressSize]byte, ok bool) {
	if len(prefix) < 2 || prefix[0] != 'T' {
		return nil, nil, false
	}
	for k := 2; k <= len(prefix); k++ {
		lo, hi, reachable := TronPrefixAddressRange(prefix[:k])
		if !reachable {
			return nil, nil, false
		}
		los = append(los, lo)
		his = append(his, hi)
	}
	return los, his, true
}

// tronLowerBound returns the smallest x in [0, tronAddrSpace) for which pred is
// true, assuming pred is monotonic (all-false then all-true). Returns
// tronAddrSpace if pred is never true.
func tronLowerBound(pred func(*big.Int) bool) *big.Int {
	lo := big.NewInt(0)
	hi := new(big.Int).Set(tronAddrSpace)
	for lo.Cmp(hi) < 0 {
		mid := new(big.Int).Add(lo, hi)
		mid.Rsh(mid, 1)
		if pred(mid) {
			hi = mid
		} else {
			lo.Add(mid, big.NewInt(1))
		}
	}
	return lo
}

func bigToAddress(x *big.Int) [AddressSize]byte {
	var out [AddressSize]byte
	x.FillBytes(out[:]) // big-endian, left-padded
	return out
}

var base58Alphabet = []byte("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz")

func TronAddressFromEVMAddress(address []byte) (string, error) {
	if len(address) != AddressSize {
		return "", fmt.Errorf("address must be %d bytes", AddressSize)
	}
	payload := make([]byte, 1+AddressSize)
	payload[0] = tronVersion
	copy(payload[1:], address)
	encoded := base58CheckEncode(payload)
	// Decode our own output and confirm it round-trips back to the same
	// 20-byte address. The hand-rolled base58check encoder is the last step
	// before the string the user copies, and the secp256k1/keccak round-trip
	// in Finalize only validates the EVM address, not this encoding — so a
	// silent bug here would hand the user an address they don't control.
	if err := verifyTronAddress(encoded, address); err != nil {
		return "", err
	}
	return encoded, nil
}

func verifyTronAddress(encoded string, address []byte) error {
	payload, err := base58CheckDecode(encoded)
	if err != nil {
		return fmt.Errorf("verify tron address: %w", err)
	}
	if len(payload) != 1+AddressSize {
		return fmt.Errorf("verify tron address: payload is %d bytes, want %d", len(payload), 1+AddressSize)
	}
	if payload[0] != tronVersion {
		return fmt.Errorf("verify tron address: version byte 0x%02x, want 0x%02x", payload[0], tronVersion)
	}
	if !equalBytes(payload[1:], address) {
		return errors.New("verify tron address: decoded address does not match")
	}
	return nil
}

func base58CheckEncode(payload []byte) string {
	first := sha256.Sum256(payload)
	second := sha256.Sum256(first[:])

	checked := make([]byte, 0, len(payload)+4)
	checked = append(checked, payload...)
	checked = append(checked, second[:4]...)
	return base58Encode(checked)
}

func base58CheckDecode(encoded string) ([]byte, error) {
	decoded, err := base58Decode(encoded)
	if err != nil {
		return nil, err
	}
	if len(decoded) < 4 {
		return nil, fmt.Errorf("base58check payload is %d bytes, want at least 4", len(decoded))
	}
	payload := decoded[:len(decoded)-4]
	checksum := decoded[len(decoded)-4:]
	first := sha256.Sum256(payload)
	second := sha256.Sum256(first[:])
	if !bytes.Equal(checksum, second[:4]) {
		return nil, errors.New("base58check checksum mismatch")
	}
	return payload, nil
}

func base58Decode(encoded string) ([]byte, error) {
	zeroes := 0
	for zeroes < len(encoded) && encoded[zeroes] == base58Alphabet[0] {
		zeroes++
	}

	// Accumulate the base58 digits into a little-endian base256 value.
	decoded := make([]byte, 0, len(encoded))
	for i := zeroes; i < len(encoded); i++ {
		value := base58Index(encoded[i])
		if value < 0 {
			return nil, fmt.Errorf("invalid base58 character %q", encoded[i])
		}
		carry := value
		for j := range decoded {
			carry += int(decoded[j]) * 58
			decoded[j] = byte(carry)
			carry >>= 8
		}
		for carry > 0 {
			decoded = append(decoded, byte(carry))
			carry >>= 8
		}
	}

	for i, j := 0, len(decoded)-1; i < j; i, j = i+1, j-1 {
		decoded[i], decoded[j] = decoded[j], decoded[i]
	}

	result := make([]byte, zeroes+len(decoded))
	copy(result[zeroes:], decoded)
	return result, nil
}

// Base58Index returns the value (0..57) of a base58 character in the
// Tron/Bitcoin alphabet, or -1 if ch is not a base58 character.
func Base58Index(ch byte) int { return base58Index(ch) }

func base58Index(ch byte) int {
	for i, a := range base58Alphabet {
		if a == ch {
			return i
		}
	}
	return -1
}

func base58Encode(value []byte) string {
	zeroes := 0
	for zeroes < len(value) && value[zeroes] == 0 {
		zeroes++
	}

	input := append([]byte(nil), value...)
	encoded := make([]byte, 0, len(value)*138/100+1)
	for start := zeroes; start < len(input); {
		remainder := 0
		for i := start; i < len(input); i++ {
			acc := remainder*256 + int(input[i])
			input[i] = byte(acc / 58)
			remainder = acc % 58
		}
		encoded = append(encoded, base58Alphabet[remainder])
		for start < len(input) && input[start] == 0 {
			start++
		}
	}

	for i := 0; i < zeroes; i++ {
		encoded = append(encoded, base58Alphabet[0])
	}
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	return string(encoded)
}
