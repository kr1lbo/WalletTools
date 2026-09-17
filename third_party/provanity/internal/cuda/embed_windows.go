//go:build windows && cudaembed

package cuda

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed assets/*.dll
var assets embed.FS

func Extract(cacheRoot, name string) (string, error) {
	data, err := assets.ReadFile("assets/" + name)
	if err != nil {
		return "", fmt.Errorf("read embedded CUDA DLL: %w", err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("embedded CUDA DLL is empty")
	}

	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	dir := filepath.Join(cacheRoot, "cuda", hash[:16])
	path := filepath.Join(dir, name)
	if info, err := os.Stat(path); err == nil && info.Size() == int64(len(data)) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create CUDA DLL cache: %w", err)
	}
	if err := os.WriteFile(path, data, 0o700); err != nil {
		return "", fmt.Errorf("write embedded CUDA DLL: %w", err)
	}
	return path, nil
}
