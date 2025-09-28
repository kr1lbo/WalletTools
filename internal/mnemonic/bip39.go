package mnemonic

import (
	"crypto/ecdsa"
	"fmt"

	hdwallet "github.com/miguelmota/go-ethereum-hdwallet"
	bip39 "github.com/tyler-smith/go-bip39"
)

type Derived struct {
	Mnemonic string
	Index    int
	Path     string
	Priv     *ecdsa.PrivateKey
	Address  string
}

func NewMnemonic(strength int) (string, error) {
	if strength == 0 {
		strength = 128 // 12 words
	}
	entropy, err := bip39.NewEntropy(strength)
	if err != nil {
		return "", err
	}
	return bip39.NewMnemonic(entropy)
}

func Derive(mn, passphrase string, n int) ([]Derived, error) {
	if n <= 0 {
		n = 5
	}
	seed := bip39.NewSeed(mn, passphrase)
	w, err := hdwallet.NewFromSeed(seed)
	if err != nil {
		return nil, err
	}
	out := make([]Derived, 0, n)
	for i := 0; i < n; i++ {
		pathStr := fmt.Sprintf("m/44'/60'/0'/0/%d", i)
		path := hdwallet.MustParseDerivationPath(pathStr)
		acct, err := w.Derive(path, true)
		if err != nil {
			return nil, err
		}
		addr, err := w.Address(acct)
		if err != nil {
			return nil, err
		}
		priv, err := w.PrivateKey(acct)
		if err != nil {
			return nil, err
		}
		out = append(out, Derived{
			Mnemonic: mn,
			Index:    i,
			Path:     pathStr,
			Priv:     priv,
			Address:  addr.Hex(),
		})
	}
	return out, nil
}
