package crypto

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestAddressFromPrivateKeyOne(t *testing.T) {
	privateKey := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000001")
	wantPublicKey := fixedHex(t,
		"79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"+
			"483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8",
	)
	wantAddress := fixedHex(t, "7e5f4552091a69125d5dfcb7b8c2659029395bdf")

	publicKey, err := PublicKeyFromPrivateKey(privateKey)
	if err != nil {
		t.Fatalf("PublicKeyFromPrivateKey returned error: %v", err)
	}
	if !bytes.Equal(publicKey, wantPublicKey) {
		t.Fatalf("public key mismatch\nwant %x\n got %x", wantPublicKey, publicKey)
	}

	address, err := AddressFromPrivateKey(privateKey)
	if err != nil {
		t.Fatalf("AddressFromPrivateKey returned error: %v", err)
	}
	if !bytes.Equal(address, wantAddress) {
		t.Fatalf("address mismatch\nwant %x\n got %x", wantAddress, address)
	}
}

func TestComposePrivateKey(t *testing.T) {
	initPrivateKey := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000001")
	offset := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000001")
	want := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000002")

	got, err := ComposePrivateKey(initPrivateKey, offset)
	if err != nil {
		t.Fatalf("ComposePrivateKey returned error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("composed private key mismatch\nwant %x\n got %x", want, got)
	}
}

func TestFinalize(t *testing.T) {
	initPrivateKey := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000001")
	offset := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000001")
	finalPrivateKey := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000002")
	expectedAddress, err := AddressFromPrivateKey(finalPrivateKey)
	if err != nil {
		t.Fatalf("AddressFromPrivateKey returned error: %v", err)
	}

	finalized, err := Finalize(initPrivateKey, offset, expectedAddress)
	if err != nil {
		t.Fatalf("Finalize returned error: %v", err)
	}
	if !bytes.Equal(finalized.PrivateKey, finalPrivateKey) {
		t.Fatalf("final private key mismatch\nwant %x\n got %x", finalPrivateKey, finalized.PrivateKey)
	}
	if !bytes.Equal(finalized.Address, expectedAddress) {
		t.Fatalf("final address mismatch\nwant %x\n got %x", expectedAddress, finalized.Address)
	}

	wrongAddress := make([]byte, AddressSize)
	if _, err := Finalize(initPrivateKey, offset, wrongAddress); err == nil {
		t.Fatal("Finalize accepted mismatched address")
	}
}

func TestRejectsInvalidScalars(t *testing.T) {
	zero := make([]byte, PrivateKeySize)
	one := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000001")
	curveOrder := fixedHex(t, "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141")
	curveOrderMinusOne := fixedHex(t, "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364140")

	if _, err := PublicKeyFromPrivateKey(zero); err == nil {
		t.Fatal("PublicKeyFromPrivateKey accepted zero private key")
	}
	if _, err := PublicKeyFromPrivateKey(curveOrder); err == nil {
		t.Fatal("PublicKeyFromPrivateKey accepted secp256k1 curve order")
	}
	if _, err := ComposePrivateKey(one, []byte{1, 2, 3}); err == nil {
		t.Fatal("ComposePrivateKey accepted short offset")
	}
	if _, err := ComposePrivateKey(curveOrderMinusOne, one); err == nil {
		t.Fatal("ComposePrivateKey accepted wrapped zero final key")
	}
}

func fixedHex(t *testing.T, value string) []byte {
	t.Helper()
	out, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode hex %q: %v", value, err)
	}
	return out
}
