// Package portable initializes a standalone installation without overwriting user data.
package portable

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed defaults/*.yaml
var defaults embed.FS

func Init(dir string) error {
	for _, sub := range []string{"configs", "inputs/encrypt", "inputs/decrypt", "logs"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			return fmt.Errorf("create %s: %w", sub, err)
		}
	}
	for _, name := range []string{"app.yaml", "patterns.yaml"} {
		data, err := defaults.ReadFile("defaults/" + name)
		if err != nil {
			return err
		}
		if err := createMissing(filepath.Join(dir, "configs", name), data); err != nil {
			return err
		}
	}
	return createMissing(filepath.Join(dir, "inputs", "encrypt", "privates.txt"), []byte("# One private key per line. Replace this comment with your own keys.\n"))
}

func createMissing(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	return errors.Join(writeErr, closeErr)
}
