package crypto

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"math/big"
	"testing"
)

func TestTronAddressFromZeroEVMAddress(t *testing.T) {
	address := make([]byte, AddressSize)
	got, err := TronAddressFromEVMAddress(address)
	if err != nil {
		t.Fatalf("TronAddressFromEVMAddress() error = %v", err)
	}
	if got != "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb" {
		t.Fatalf("Tron address = %s", got)
	}
}

func TestTronAddressRejectsWrongSize(t *testing.T) {
	if _, err := TronAddressFromEVMAddress([]byte{1, 2, 3}); err == nil {
		t.Fatal("TronAddressFromEVMAddress() succeeded, want error")
	}
}

func TestTronAddressRoundTrip(t *testing.T) {
	for range 256 {
		address := make([]byte, AddressSize)
		if _, err := rand.Read(address); err != nil {
			t.Fatalf("rand.Read() error = %v", err)
		}
		encoded, err := TronAddressFromEVMAddress(address)
		if err != nil {
			t.Fatalf("TronAddressFromEVMAddress() error = %v", err)
		}
		payload, err := base58CheckDecode(encoded)
		if err != nil {
			t.Fatalf("base58CheckDecode(%q) error = %v", encoded, err)
		}
		if payload[0] != tronVersion {
			t.Fatalf("version byte = 0x%02x, want 0x%02x", payload[0], tronVersion)
		}
		if !bytes.Equal(payload[1:], address) {
			t.Fatalf("round-trip mismatch\nwant %x\n got %x", address, payload[1:])
		}
	}
}

func TestTronPrefixAddressRange(t *testing.T) {
	addrString := func(x *big.Int) string {
		addr := bigToAddress(x)
		s, err := TronAddressFromEVMAddress(addr[:])
		if err != nil {
			t.Fatalf("TronAddressFromEVMAddress error = %v", err)
		}
		return s
	}
	for _, prefix := range []string{"TA", "TR", "TLa", "TWxyz", "TKabc"} {
		lo, hi, ok := TronPrefixAddressRange(prefix)
		if !ok {
			t.Fatalf("TronPrefixAddressRange(%q) ok = false, want reachable", prefix)
		}
		loInt := new(big.Int).SetBytes(lo[:])
		hiInt := new(big.Int).SetBytes(hi[:])
		if loInt.Cmp(hiInt) > 0 {
			t.Fatalf("%q: lo > hi", prefix)
		}
		if got := addrString(loInt)[:len(prefix)]; got != prefix {
			t.Fatalf("%q: A(lo) prefix = %q", prefix, got)
		}
		if got := addrString(hiInt)[:len(prefix)]; got != prefix {
			t.Fatalf("%q: A(hi) prefix = %q", prefix, got)
		}
		if loInt.Sign() > 0 {
			below := new(big.Int).Sub(loInt, big.NewInt(1))
			if addrString(below)[:len(prefix)] == prefix {
				t.Fatalf("%q: address below lo still matches prefix", prefix)
			}
		}
		above := new(big.Int).Add(hiInt, big.NewInt(1))
		if above.Cmp(tronAddrSpace) < 0 && addrString(above)[:len(prefix)] == prefix {
			t.Fatalf("%q: address above hi still matches prefix", prefix)
		}
	}
	// Prefixes outside the reachable [min, max] range must be rejected.
	for _, prefix := range []string{"TZZ", "T9a", "Tz"} {
		if _, _, ok := TronPrefixAddressRange(prefix); ok {
			t.Fatalf("TronPrefixAddressRange(%q) ok = true, want unreachable", prefix)
		}
	}
}

func TestTronPrefixLadder(t *testing.T) {
	addrString := func(b [AddressSize]byte) string {
		s, err := TronAddressFromEVMAddress(b[:])
		if err != nil {
			t.Fatalf("TronAddressFromEVMAddress error = %v", err)
		}
		return s
	}
	const prefix = "TWxyz" // T + "Wxyz" -> 4 ladder levels
	los, his, ok := TronPrefixLadder(prefix)
	if !ok {
		t.Fatalf("TronPrefixLadder(%q) unreachable", prefix)
	}
	if len(los) != len(prefix)-1 || len(his) != len(prefix)-1 {
		t.Fatalf("levels = %d/%d, want %d", len(los), len(his), len(prefix)-1)
	}
	for j := range los {
		want := prefix[:j+2] // T + first j+1 concrete characters
		loInt := new(big.Int).SetBytes(los[j][:])
		hiInt := new(big.Int).SetBytes(his[j][:])
		if loInt.Cmp(hiInt) > 0 {
			t.Fatalf("level %d: lo > hi", j)
		}
		if got := addrString(los[j])[:len(want)]; got != want {
			t.Fatalf("level %d: A(lo) prefix = %q, want %q", j, got, want)
		}
		if got := addrString(his[j])[:len(want)]; got != want {
			t.Fatalf("level %d: A(hi) prefix = %q, want %q", j, got, want)
		}
		if j > 0 {
			// Each deeper interval must be contained in the previous one.
			prevLo := new(big.Int).SetBytes(los[j-1][:])
			prevHi := new(big.Int).SetBytes(his[j-1][:])
			if loInt.Cmp(prevLo) < 0 || hiInt.Cmp(prevHi) > 0 {
				t.Fatalf("level %d interval not nested in level %d", j, j-1)
			}
		}
	}
	if _, _, ok := TronPrefixLadder("Tz"); ok {
		t.Fatal("TronPrefixLadder(Tz) ok = true, want unreachable")
	}
}

// TestTronSuffixDigitsMatchEncoding cross-checks the device suffix math (recover
// the trailing base58 digits from value mod 58^N) against the real base58check
// encoding, so a kernel bug in that path would be caught by the round-trip.
func TestTronSuffixDigitsMatchEncoding(t *testing.T) {
	const n = 8
	mod := new(big.Int).Exp(big.NewInt(58), big.NewInt(n), nil)
	for range 64 {
		address := make([]byte, AddressSize)
		if _, err := rand.Read(address); err != nil {
			t.Fatalf("rand.Read error = %v", err)
		}
		encoded, err := TronAddressFromEVMAddress(address)
		if err != nil {
			t.Fatalf("TronAddressFromEVMAddress error = %v", err)
		}
		payload := make([]byte, 1+AddressSize)
		payload[0] = tronVersion
		copy(payload[1:], address)
		first := sha256.Sum256(payload)
		second := sha256.Sum256(first[:])
		full := append(append([]byte{}, payload...), second[:4]...)

		r := new(big.Int).Mod(new(big.Int).SetBytes(full), mod)
		digit := new(big.Int)
		for i := range n {
			r.DivMod(r, big.NewInt(58), digit)
			if got := base58Alphabet[digit.Int64()]; got != encoded[len(encoded)-1-i] {
				t.Fatalf("trailing digit %d = %c, want %c (addr %s)", i, got, encoded[len(encoded)-1-i], encoded)
			}
		}
	}
}

func TestBase58CheckDecodeRejectsCorruption(t *testing.T) {
	encoded := "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb"

	if _, err := base58CheckDecode(encoded[:len(encoded)-1] + "c"); err == nil {
		t.Fatal("base58CheckDecode() accepted a flipped checksum character")
	}
	if _, err := base58CheckDecode(encoded + "0"); err == nil {
		t.Fatal("base58CheckDecode() accepted an invalid base58 character")
	}
}
