package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsFromBase(t *testing.T) {
	paths, err := ResolvePathsFromBase(filepath.Join("C:", "cfg"), filepath.Join("C:", "cache"), "provanity")
	if err != nil {
		t.Fatalf("ResolvePathsFromBase() error = %v", err)
	}

	if paths.ConfigDir != filepath.Join("C:", "cfg", "provanity") {
		t.Fatalf("ConfigDir = %q", paths.ConfigDir)
	}
	if paths.BinaryCacheDir != filepath.Join("C:", "cache", "provanity", "cache") {
		t.Fatalf("BinaryCacheDir = %q", paths.BinaryCacheDir)
	}
}

func TestResolvePathsRejectsPathLikeAppName(t *testing.T) {
	if _, err := ResolvePathsFromBase("cfg", "cache", "../provanity"); err == nil {
		t.Fatal("expected invalid app name error")
	}
}

func TestEnsureCreatesDirectories(t *testing.T) {
	root := t.TempDir()
	paths, err := ResolvePathsFromBase(filepath.Join(root, "cfg"), filepath.Join(root, "cache"), "provanity")
	if err != nil {
		t.Fatalf("ResolvePathsFromBase() error = %v", err)
	}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	for _, dir := range []string{
		paths.ConfigDir,
		paths.CacheDir,
		paths.ProfilesDir,
		paths.StateDir,
		paths.LogsDir,
		paths.BinaryCacheDir,
	} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
	}
}
