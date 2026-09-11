package encdec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gethks "github.com/ethereum/go-ethereum/accounts/keystore"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	base := t.TempDir()
	inputs := filepath.Join(base, "inputs")
	logs := filepath.Join(base, "logs")
	if err := os.MkdirAll(filepath.Join(inputs, "encrypt"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(inputs, "decrypt"), 0700); err != nil {
		t.Fatal(err)
	}
	// Public test fixture, never a funded wallet.
	private := strings.Repeat("0", 63) + "1"
	if err := os.WriteFile(filepath.Join(inputs, "encrypt", "privates.txt"), []byte("# fixture\n0x"+private+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	password := " test password "
	if err := EncryptPrivates(context.Background(), EncryptOptions{InputsBaseDir: inputs, LogsBase: logs, Password: password, HideSecretsInConsole: true}); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(logs, "encrypt", "*", "*", "all.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("expected encrypted output: %v", err)
	}
	blob, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inputs, "decrypt", "specific.jsonl"), blob, 0600); err != nil {
		t.Fatal(err)
	}
	if err := DecryptKeystores(context.Background(), DecryptOptions{InputsBaseDir: inputs, LogsBase: logs, Password: password, HideSecretsInConsole: true}); err != nil {
		t.Fatal(err)
	}
	outputs, _ := filepath.Glob(filepath.Join(logs, "decrypt", "*", "*", "all.txt"))
	if len(outputs) != 1 {
		t.Fatal("missing decrypted output")
	}
	data, err := os.ReadFile(outputs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(data)), ":0x"+private) {
		t.Fatal("round trip changed the key")
	}
	if err := DecryptKeystores(context.Background(), DecryptOptions{InputsBaseDir: inputs, LogsBase: logs, Password: "wrong", HideSecretsInConsole: true}); err == nil {
		t.Fatal("wrong password returned success")
	}
	unchanged, _ := os.ReadFile(outputs[0])
	if string(unchanged) != string(data) {
		t.Fatal("later run overwrote previous output")
	}
}

func TestMalformedKeystoreReturnsError(t *testing.T) {
	if _, _, err := decryptOne([]byte(`{"version":3,"id":"00000000-0000-0000-0000-000000000000","crypto":{"cipher":"aes-128-ctr","kdf":"scrypt","kdfparams":{}}}`), "test"); err == nil {
		t.Fatal("expected malformed keystore error")
	}
}

func TestDecryptBatchCancellationAndEmptyInput(t *testing.T) {
	base := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := operationResult(ctx, 1, 1, 0); err != context.Canceled {
		t.Fatal("cancellation lost")
	}
	if err := DecryptKeystores(context.Background(), DecryptOptions{InputsBaseDir: base, LogsBase: filepath.Join(base, "logs"), Password: "test"}); err == nil {
		t.Fatal("empty batch returned success")
	}
}

func TestDecryptUsesAddressDerivedFromPrivateKey(t *testing.T) {
	priv, err := gethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	blob, err := gethks.EncryptKey(&gethks.Key{Address: gethcrypto.PubkeyToAddress(priv.PublicKey), PrivateKey: priv}, "test", gethks.LightScryptN, gethks.LightScryptP)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(blob, &fields); err != nil {
		t.Fatal(err)
	}
	fields["address"] = strings.Repeat("0", 40)
	blob, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	addr, _, err := decryptOne(blob, "test")
	if err != nil {
		t.Fatal(err)
	}
	if addr != gethcrypto.PubkeyToAddress(priv.PublicKey).Hex() {
		t.Fatal("trusted unauthenticated address metadata")
	}
}

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
