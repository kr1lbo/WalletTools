package appcfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMasksSecretsByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte("log_level: info\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HideSecretsInConsole {
		t.Fatal("omitted setting must not expose secrets")
	}
}

func TestLoadSupportsLegacyHideSecretsKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte("hide_secrets: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HideSecretsInConsole {
		t.Fatal("expected legacy hide_secrets to enable console secret masking")
	}
}

func TestLoadPrefersHideSecretsInConsole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.yaml")
	data := []byte("hide_secrets: true\nhide_secrets_in_console: false\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HideSecretsInConsole {
		t.Fatal("expected hide_secrets_in_console to take precedence")
	}
}

func TestLoadCUDASettingsAndDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.yaml")
	data := []byte("gpu_enabled: true\ncuda_device: 2\ncuda_batch_size: 4096\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.GPUEnabled || cfg.CUDADevice != 2 || cfg.CUDABatchSize != 4096 {
		t.Fatalf("CUDA settings were not loaded: %+v", cfg)
	}
	if cfg.CUDAExecutable != "wallettools-cuda.exe" {
		t.Fatalf("unexpected default helper: %q", cfg.CUDAExecutable)
	}
}
