package portable

import (
	"os"
	"path/filepath"
	"testing"

	"WalletTools/pkg/appcfg"
	"WalletTools/pkg/config"
)

func TestFreshInstallAndUpgrade(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	app, err := appcfg.Load(filepath.Join(dir, "configs", "app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !app.HideSecretsInConsole {
		t.Fatal("fresh installs must mask secrets")
	}
	if _, err := config.Load(filepath.Join(dir, "configs", "patterns.yaml")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"configs/app.yaml", "configs/patterns.yaml", "inputs/encrypt/privates.txt", "inputs/decrypt/user.json", "logs/user.txt"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("user data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"configs/app.yaml", "configs/patterns.yaml", "inputs/encrypt/privates.txt", "inputs/decrypt/user.json", "logs/user.txt"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != "user data" {
			t.Fatalf("overwrote %s: %v", name, err)
		}
	}
}

func TestInvalidDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Init(path); err == nil {
		t.Fatal("expected error for file as data directory")
	}
}
