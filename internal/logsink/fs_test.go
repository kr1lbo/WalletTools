package logsink

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunsNeverShareOutputDirectory(t *testing.T) {
	base := t.TempDir()
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		dir, err := MakeModuleDirs(base, "decrypt", true)
		if err != nil {
			t.Fatal(err)
		}
		if seen[dir] {
			t.Fatal("run reuses an existing output directory")
		}
		seen[dir] = true
		if err := os.WriteFile(filepath.Join(dir, "all.txt"), []byte("existing output"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
