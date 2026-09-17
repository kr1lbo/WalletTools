package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/crypto/sha3"
)

const (
	PrivateKeySize = 32
	PublicKeySize  = 64
	AddressSize    = 20
)

type KeyPair struct {
	PrivateKey []byte
	PublicKey  []byte
	Address    []byte
}

type FinalizedKey struct {
	PrivateKey []byte
	PublicKey  []byte
	Address    []byte
}

func GenerateKeyPair() (KeyPair, error) {
	return GenerateKeyPairFromRand(rand.Reader)
}

func GenerateKeyPairFromRand(r io.Reader) (KeyPair, error) {
	priv, err := secp256k1.GeneratePrivateKeyFromRand(r)
	if err != nil {
		return KeyPair{}, err
	}

	privBytes := priv.Serialize()
	pubBytes := serializePublicKeyXY(priv.PubKey())
	address, err := AddressFromPublicKey(pubBytes)
	if err != nil {
		return KeyPair{}, err
	}

	return KeyPair{
		PrivateKey: append([]byte(nil), privBytes...),
		PublicKey:  pubBytes,
		Address:    address,
	}, nil
}

func PublicKeyFromPrivateKey(privateKey []byte) ([]byte, error) {
	scalar, err := parseScalar("private key", privateKey, false)
	if err != nil {
		return nil, err
	}
	priv := secp256k1.NewPrivateKey(&scalar)
	return serializePublicKeyXY(priv.PubKey()), nil
}

func AddressFromPrivateKey(privateKey []byte) ([]byte, error) {
	pub, err := PublicKeyFromPrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	return AddressFromPublicKey(pub)
}

func AddressFromPublicKey(publicKey []byte) ([]byte, error) {
	pub, err := ParsePublicKeyXY(publicKey)
	if err != nil {
		return nil, err
	}

	serialized := pub.SerializeUncompressed()
	hash := sha3.NewLegacyKeccak256()
	if _, err := hash.Write(serialized[1:]); err != nil {
		return nil, err
	}
	sum := hash.Sum(nil)
	return append([]byte(nil), sum[12:]...), nil
}

func ParsePublicKeyXY(publicKey []byte) (*secp256k1.PublicKey, error) {
	if len(publicKey) != PublicKeySize {
		return nil, fmt.Errorf("public key must be %d bytes", PublicKeySize)
	}
	serialized := make([]byte, 1+PublicKeySize)
	serialized[0] = 0x04
	copy(serialized[1:], publicKey)
	pub, err := secp256k1.ParsePubKey(serialized)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	return pub, nil
}

func ComposePrivateKey(initPrivateKey, offset []byte) ([]byte, error) {
	initScalar, err := parseScalar("initial private key", initPrivateKey, false)
	if err != nil {
		return nil, err
	}
	offsetScalar, err := parseScalar("offset", offset, true)
	if err != nil {
		return nil, err
	}

	var finalScalar secp256k1.ModNScalar
	finalScalar.Add2(&initScalar, &offsetScalar)
	if finalScalar.IsZero() {
		return nil, errors.New("final private key is zero")
	}

	finalBytes := finalScalar.Bytes()
	return append([]byte(nil), finalBytes[:]...), nil
}

func Finalize(initPrivateKey, offset, expectedAddress []byte) (FinalizedKey, error) {
	if len(expectedAddress) != AddressSize {
		return FinalizedKey{}, fmt.Errorf("expected address must be %d bytes", AddressSize)
	}

	finalPrivateKey, err := ComposePrivateKey(initPrivateKey, offset)
	if err != nil {
		return FinalizedKey{}, err
	}
	publicKey, err := PublicKeyFromPrivateKey(finalPrivateKey)
	if err != nil {
		return FinalizedKey{}, err
	}
	address, err := AddressFromPublicKey(publicKey)
	if err != nil {
		return FinalizedKey{}, err
	}
	if !equalBytes(address, expectedAddress) {
		return FinalizedKey{}, errors.New("final address does not match expected address")
	}

	return FinalizedKey{
		PrivateKey: finalPrivateKey,
		PublicKey:  publicKey,
		Address:    address,
	}, nil
}

func parseScalar(name string, value []byte, allowZero bool) (secp256k1.ModNScalar, error) {
	var scalar secp256k1.ModNScalar
	if len(value) != PrivateKeySize {
		return scalar, fmt.Errorf("%s must be %d bytes", name, PrivateKeySize)
	}

	var b32 [PrivateKeySize]byte
	copy(b32[:], value)
	overflow := scalar.SetBytes(&b32)
	for i := range b32 {
		b32[i] = 0
	}
	if overflow != 0 {
		return scalar, fmt.Errorf("%s must be less than secp256k1n", name)
	}
	if !allowZero && scalar.IsZero() {
		return scalar, fmt.Errorf("%s cannot be zero", name)
	}
	return scalar, nil
}

func serializePublicKeyXY(publicKey *secp256k1.PublicKey) []byte {
	serialized := publicKey.SerializeUncompressed()
	return append([]byte(nil), serialized[1:]...)
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
