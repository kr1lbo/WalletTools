package encdec

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"WalletTools/internal/keystore"
	"WalletTools/internal/logsink"
	"WalletTools/pkg/logx"

	gethks "github.com/ethereum/go-ethereum/accounts/keystore"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// EncryptOptions controls encryption job behaviour.
type EncryptOptions struct {
	LogLevel             string
	InputsBaseDir        string // e.g. "inputs"
	LogsBase             string // e.g. "logs"
	Password             string // required
	PassHint             string // optional text stored near logs for future reference
	HideSecretsInConsole bool   // if true, do not print private keys to console logs
}

// DecryptOptions controls decryption job behaviour.
type DecryptOptions struct {
	LogLevel             string
	InputsBaseDir        string // e.g. "inputs"
	LogsBase             string // e.g. "logs"
	Password             string // required
	HideSecretsInConsole bool
}

// EncryptPrivates reads inputs/encrypt/privates.txt and encrypts each
// private key with a single password. Results:
//
//	logs/encrypt/<DD.MM.YYYY>/encrypt_<HH-MM-SS>/app.log
//	logs/encrypt/.../all.jsonl (one keystore JSON per line)
//	logs/encrypt/.../files/<address>.json (one file per wallet)
func EncryptPrivates(ctx context.Context, opt EncryptOptions) error {
	const module = "encrypt"

	if opt.Password == "" {
		return errors.New("keystore password must not be empty")
	}

	dir, err := logsink.MakeModuleDirs(opt.LogsBase, module, true)
	if err != nil {
		return err
	}
	// optional hint for the operator
	if err := logsink.WriteHint(dir, opt.PassHint); err != nil {
		return fmt.Errorf("write hint: %w", err)
	}

	logPath := filepath.Join(dir, "app.log")
	if err := logx.Init(logx.Config{Level: opt.LogLevel, FilePath: logPath, ConsoleOnly: false, HideSecretsInConsole: opt.HideSecretsInConsole}); err != nil {
		return fmt.Errorf("logx init failed: %w", err)
	}
	defer logx.Close()
	app := logx.S()

	inFile := filepath.Join(opt.InputsBaseDir, "encrypt", "privates.txt")
	f, err := os.Open(inFile)
	if err != nil {
		return fmt.Errorf("open privates.txt: %w", err)
	}
	defer f.Close()

	filesDir := filepath.Join(dir, "files")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		return fmt.Errorf("mkdir files: %w", err)
	}

	app.Infow("encrypt started", "inputs", inFile, "out", dir)

	reader := bufio.NewReader(f)
	allPath := filepath.Join(dir, "all.jsonl")

	var total, okCnt, failCnt int
	start := time.Now()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read private keys: %w", err)
		}
		raw := strings.TrimSpace(line)
		if raw == "" || strings.HasPrefix(raw, "#") {
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}
		total++

		hex := strings.TrimPrefix(strings.TrimPrefix(raw, "0x"), "0X")
		priv, perr := gethcrypto.HexToECDSA(hex)
		if perr != nil {
			failCnt++
			app.Errorw("parse private key failed", "err", perr)
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}

		addr := gethcrypto.PubkeyToAddress(priv.PublicKey).Hex() // keep 0x prefix
		blob, kerr := gethks.EncryptKey(&gethks.Key{Address: gethcrypto.PubkeyToAddress(priv.PublicKey), PrivateKey: priv}, opt.Password, gethks.StandardScryptN, gethks.StandardScryptP)
		if kerr != nil {
			failCnt++
			app.Errorw("keystore encrypt failed", "addr", addr, "err", kerr)
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}

		// Force 0x prefix in keystore JSON before persisting
		if patched, perr := forceAddressPrefix(blob, true); perr == nil {
			blob = patched
		}

		if err := keystore.AppendJSONL(allPath, blob); err != nil {
			failCnt++
			app.Errorw("append jsonl failed", "addr", addr, "err", err)
			continue
		}

		perWallet := filepath.Join(filesDir, strings.ToLower(strings.TrimPrefix(addr, "0x"))+".json")
		if werr := os.WriteFile(perWallet, blob, 0o600); werr != nil {
			failCnt++
			app.Errorw("write single keystore failed", "addr", addr, "err", werr)
			continue
		}

		okCnt++
		if !opt.HideSecretsInConsole {
			app.Infow("ENCRYPTED", "address", addr, "private_key", "0x"+fmt.Sprintf("%x", gethcrypto.FromECDSA(priv)))
		} else {
			app.Infow("ENCRYPTED", "address", addr)
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	app.Infow("encrypt finished", "total", total, "ok", okCnt, "failed", failCnt, "elapsed", time.Since(start).String())
	return operationResult(ctx, total, okCnt, failCnt)
}

// DecryptKeystores reads inputs/decrypt/{all.jsonl, *.json, files/*.json}
// and writes raw keys into logs/decrypt/.../all.txt as "address:private" lines.
func DecryptKeystores(ctx context.Context, opt DecryptOptions) error {
	const module = "decrypt"

	if opt.Password == "" {
		return errors.New("keystore password must not be empty")
	}

	dir, err := logsink.MakeModuleDirs(opt.LogsBase, module, true)
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, "app.log")
	if err := logx.Init(logx.Config{Level: opt.LogLevel, FilePath: logPath, ConsoleOnly: false, HideSecretsInConsole: opt.HideSecretsInConsole}); err != nil {
		return fmt.Errorf("logx init failed: %w", err)
	}
	defer logx.Close()
	app := logx.S()

	inDir := filepath.Join(opt.InputsBaseDir, "decrypt")
	outAll := filepath.Join(dir, "all.txt")

	outF, err := os.OpenFile(outAll, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create all.txt: %w", err)
	}
	defer outF.Close()

	files := collectInputFiles(inDir)
	if len(files) == 0 {
		app.Warnw("no keystore files found", "dir", inDir)
		return errors.New("no keystore files found in inputs/decrypt")
	}

	app.Infow("decrypt started", "inputs", inDir, "out", dir, "files", len(files))

	writeLine := func(addr, privHex string) error {
		_, err := fmt.Fprintf(outF, "%s:%s\n", addr, privHex)
		return err
	}

	var total, okCnt, failCnt int
	start := time.Now()

	for _, p := range files {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if strings.HasSuffix(p, ".jsonl") {
			f, err := os.Open(p)
			if err != nil {
				failCnt++
				app.Errorw("open jsonl failed", "file", p, "err", err)
				continue
			}
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 4096), 4*1024*1024)
			for sc.Scan() {
				if ctx.Err() != nil {
					_ = f.Close()
					return ctx.Err()
				}
				line := strings.TrimSpace(sc.Text())
				if line == "" {
					continue
				}
				total++
				addr, privHex, derr := decryptOne([]byte(line), opt.Password)
				if derr != nil {
					failCnt++
					app.Errorw("decrypt failed", "file", p, "err", derr)
					continue
				}
				okCnt++
				if werr := writeLine(addr, privHex); werr != nil {
					failCnt++
					okCnt--
					app.Errorw("write decrypted key failed", "file", p, "err", werr)
					continue
				}
				if !opt.HideSecretsInConsole {
					app.Infow("DECRYPTED", "address", addr, "private_key", privHex)
				} else {
					app.Infow("DECRYPTED", "address", addr)
				}
			}
			_ = f.Close()
			if err := sc.Err(); err != nil {
				failCnt++
				app.Errorw("scan jsonl failed", "file", p, "err", err)
			}
			continue
		}

		blob, err := os.ReadFile(p)
		if err != nil {
			failCnt++
			app.Errorw("read json failed", "file", p, "err", err)
			continue
		}
		total++
		addr, privHex, derr := decryptOne(blob, opt.Password)
		if derr != nil {
			failCnt++
			app.Errorw("decrypt failed", "file", p, "err", derr)
			continue
		}
		okCnt++
		if werr := writeLine(addr, privHex); werr != nil {
			failCnt++
			okCnt--
			app.Errorw("write decrypted key failed", "file", p, "err", werr)
			continue
		}
		if !opt.HideSecretsInConsole {
			app.Infow("DECRYPTED", "address", addr, "private_key", privHex)
		} else {
			app.Infow("DECRYPTED", "address", addr)
		}
	}

	app.Infow("decrypt finished", "total", total, "ok", okCnt, "failed", failCnt, "elapsed", time.Since(start).String())
	if err := outF.Close(); err != nil {
		return fmt.Errorf("close decrypted output: %w", err)
	}
	return operationResult(ctx, total, okCnt, failCnt)
}

func operationResult(ctx context.Context, total, ok, failed int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if failed > 0 {
		return fmt.Errorf("operation incomplete: %d succeeded, %d failed; see app.log", ok, failed)
	}
	if total == 0 {
		return errors.New("no input records found")
	}
	return nil
}

func collectInputFiles(inDir string) []string {
	var files []string
	entries, _ := os.ReadDir(inDir)
	for _, de := range entries {
		if de.IsDir() {
			// support inputs/decrypt/files/*.json
			if de.Name() == "files" {
				sub := filepath.Join(inDir, "files")
				subEntries, _ := os.ReadDir(sub)
				for _, se := range subEntries {
					if !se.IsDir() && strings.HasSuffix(se.Name(), ".json") {
						files = append(files, filepath.Join(sub, se.Name()))
					}
				}
			}
			continue
		}
		if strings.HasSuffix(de.Name(), ".json") || strings.HasSuffix(de.Name(), ".jsonl") {
			files = append(files, filepath.Join(inDir, de.Name()))
		}
	}
	return files
}

func decryptOne(blob []byte, password string) (addr string, privHex string, err error) {
	// The upstream decoder assumes some KDF fields have valid JSON types.
	// A malformed input file must not terminate the entire batch.
	defer func() {
		if recover() != nil {
			addr, privHex, err = "", "", errors.New("malformed keystore parameters")
		}
	}()
	blob = []byte(strings.TrimSpace(string(blob)))
	// Validate JSON ahead of DecryptKey to return clearer error on garbage input.
	var js map[string]any
	if err := json.Unmarshal(blob, &js); err != nil {
		return "", "", fmt.Errorf("invalid keystore json: %w", err)
	}

	// Geth derives the address from the decrypted key and ignores address metadata.
	key, err := gethks.DecryptKey(blob, password)
	if err != nil {
		return "", "", err
	}
	addr = gethcrypto.PubkeyToAddress(key.PrivateKey.PublicKey).Hex()
	privHex = "0x" + fmt.Sprintf("%x", gethcrypto.FromECDSA(key.PrivateKey))
	return addr, privHex, nil
}

// forceAddressPrefix rewrites the top-level "address" field in a keystore V3 JSON.
// If want0x=true, ensures it has 0x; if false, strips 0x if present.
func forceAddressPrefix(blob []byte, want0x bool) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(blob, &m); err != nil {
		return nil, err
	}
	addr, _ := m["address"].(string)
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return blob, nil
	}
	lower := strings.ToLower(addr)
	if want0x {
		if !strings.HasPrefix(lower, "0x") {
			lower = "0x" + lower
		}
	} else {
		lower = strings.TrimPrefix(lower, "0x")
	}
	m["address"] = lower
	patched, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return patched, nil
}
