package cudagen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamRejectsInvalidOptions(t *testing.T) {
	for _, opt := range []Options{
		{},
		{Executable: "helper", Device: -1, BatchSize: 1},
		{Executable: "helper", Device: 0, BatchSize: 0},
	} {
		if _, _, err := Stream(context.Background(), opt); err == nil {
			t.Fatalf("accepted invalid options: %+v", opt)
		}
	}
}

func TestStreamResolvesHelperFromCurrentDirectory(t *testing.T) {
	if os.PathSeparator != '\\' {
		t.Skip("Windows executable lookup behavior")
	}
	dir := t.TempDir()
	name := "custom-broken-helper.exe"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("not an executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	_, _, err = Stream(context.Background(), Options{Executable: name, BatchSize: 256})
	if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, name)) {
		t.Fatalf("helper was not resolved to an absolute local path: %v", err)
	}
}

func TestResolveExecutableExtractsEmbeddedDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	resolved, err := resolveExecutable("wallettools-cuda.exe")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatalf("embedded helper was not extracted: %v", err)
	}
	if len(data) != len(embeddedHelper) {
		t.Fatalf("extracted helper size = %d, want %d", len(data), len(embeddedHelper))
	}
}
