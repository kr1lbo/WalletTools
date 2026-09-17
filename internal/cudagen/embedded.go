package cudagen

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed assets/wallettools-cuda.exe
var embeddedHelper []byte

func embeddedExecutable() (string, error) {
	sum := sha256.Sum256(embeddedHelper)
	version := hex.EncodeToString(sum[:8])
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		cacheRoot = os.TempDir()
	}
	dir := filepath.Join(cacheRoot, "WalletTools", "cuda", version)
	target := filepath.Join(dir, "wallettools-cuda.exe")

	if data, readErr := os.ReadFile(target); readErr == nil && sha256.Sum256(data) == sum {
		return target, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create CUDA cache: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "wallettools-cuda-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary CUDA helper: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(embeddedHelper); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("extract CUDA helper: %w", err)
	}
	if err := os.Chmod(tmpName, 0o700); err != nil {
		return "", fmt.Errorf("make CUDA helper executable: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		// Another WalletTools process may have won the extraction race.
		if data, readErr := os.ReadFile(target); readErr == nil && sha256.Sum256(data) == sum {
			return target, nil
		}
		_ = os.Remove(target)
		if err := os.Rename(tmpName, target); err != nil {
			return "", fmt.Errorf("install CUDA helper: %w", err)
		}
	}
	return target, nil
}
