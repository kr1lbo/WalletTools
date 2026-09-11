package keystore

import (
	"errors"
	"os"
	"path/filepath"
)

func AppendJSONL(path string, jsonBlob []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(jsonBlob); err != nil {
		_ = f.Close()
		return err
	}
	_, err = f.Write([]byte("\n"))
	return errors.Join(err, f.Close())
}
