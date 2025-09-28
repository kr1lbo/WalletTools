package logsink

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func MakeModuleDirs(base, module string, keystore bool) (string, error) {
	now := time.Now()
	date := now.Format("02.01.2006")
	timeDir := now.Format("15-04-05")

	name := module + "_" + timeDir
	if keystore && module == "private" {
		name = module + "_keystore_" + timeDir
	}

	dir := filepath.Join(base, module, date, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %q: %w", dir, err)
	}
	return dir, nil
}

func OpenAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}
