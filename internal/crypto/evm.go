package crypto

import (
	"crypto/ecdsa"
	"fmt"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func NewPrivKey() (*ecdsa.PrivateKey, error) {
	return gethcrypto.GenerateKey()
}

func PrivToHex(priv *ecdsa.PrivateKey) string {
	return "0x" + fmt.Sprintf("%x", gethcrypto.FromECDSA(priv))
}

func AddressHex(priv *ecdsa.PrivateKey) string {
	return gethcrypto.PubkeyToAddress(priv.PublicKey).Hex()
}

func KeystoreJSON(priv *ecdsa.PrivateKey, password string) ([]byte, error) {
	key := &keystore.Key{
		Address:    gethcrypto.PubkeyToAddress(priv.PublicKey),
		PrivateKey: priv,
	}
	return keystore.EncryptKey(key, password, keystore.StandardScryptN, keystore.StandardScryptP)
}
