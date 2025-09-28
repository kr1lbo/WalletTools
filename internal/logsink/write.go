package logsink

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func WriteMatch(dir, kind string, payload interface{}, asJSON bool) error {
	var fname string
	switch kind {
	case "symmetric", "specific", "edges", "regexp":
		if asJSON {
			fname = kind + ".json"
		} else {
			fname = kind + ".log"
		}
	case "app":
		fname = "app.log"
	default:
		fname = kind + ".log"
	}
	path := filepath.Join(dir, fname)
	f, err := OpenAppend(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if asJSON {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_, err = f.Write(append(b, '\n'))
		return err
	}

	switch v := payload.(type) {
	case string:
		_, err = f.WriteString(v + "\n")
	default:
		b, _ := json.Marshal(payload)
		_, err = f.Write(append(b, '\n'))
	}
	return err
}

func WriteHint(dir, hint string) error {
	if hint == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, "hint.txt"), []byte(hint), 0o600)
}
