package encdec

import (
	"fmt"
	"testing"

	gethks "github.com/ethereum/go-ethereum/accounts/keystore"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestDecryptOneSupportsPrefixedKeystoreAddress(t *testing.T) {
	const password = "test-password"

	priv, err := gethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := gethcrypto.PubkeyToAddress(priv.PublicKey)
	blob, err := gethks.EncryptKey(&gethks.Key{Address: addr, PrivateKey: priv}, password, gethks.LightScryptN, gethks.LightScryptP)
	if err != nil {
		t.Fatal(err)
	}
	blob, err = forceAddressPrefix(blob, true)
	if err != nil {
		t.Fatal(err)
	}

	gotAddr, gotPriv, err := decryptOne(blob, password)
	if err != nil {
		t.Fatal(err)
	}
	wantPriv := "0x" + fmt.Sprintf("%x", gethcrypto.FromECDSA(priv))
	if gotAddr != addr.Hex() {
		t.Fatalf("address mismatch: got %s want %s", gotAddr, addr.Hex())
	}
	if gotPriv != wantPriv {
		t.Fatalf("private key mismatch: got %s want %s", gotPriv, wantPriv)
	}
}
