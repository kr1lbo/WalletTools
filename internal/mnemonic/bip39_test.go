package mnemonic

import "testing"

const testMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

func TestDeriveKnownEthereumAddress(t *testing.T) {
	derived, err := Derive(testMnemonic, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if derived[0].Address != "0x9858EfFD232B4033E47d90003D41EC34EcaEda94" {
		t.Fatal("Ethereum derivation changed")
	}
}

func TestPassphraseNormalizationAndSeparation(t *testing.T) {
	a, err := Derive(testMnemonic, "caf\u00e9", 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Derive(testMnemonic, "cafe\u0301", 1)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Derive(testMnemonic, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if a[0].Address != b[0].Address {
		t.Fatal("equivalent Unicode passphrases derive different addresses")
	}
	if a[0].Address == c[0].Address {
		t.Fatal("passphrase did not affect derivation")
	}
}
