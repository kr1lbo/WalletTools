package generator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerationSavesFinalMatches(t *testing.T) {
	for _, source := range []Source{SourcePrivKey, SourceMnemonic} {
		t.Run(string(source), func(t *testing.T) {
			base := t.TempDir()
			patternPath := filepath.Join(base, "patterns.yaml")
			if err := os.WriteFile(patternPath, []byte("symbols: ABCDEF0123456789\nregexp:\n  - pattern: '^0x'\n    final: true\n"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := Run(ctx, Options{Source: source, PatternsPath: patternPath, LogsBase: base, Workers: 1, CaseMaskedOut: true, DeriveN: 1})
			if err != nil {
				t.Fatal(err)
			}
			ext := ".jsonl"
			if source == SourceMnemonic {
				ext = ".log"
			}
			files, _ := filepath.Glob(filepath.Join(base, string(source), "*", "*", "regexp"+ext))
			if len(files) != 1 {
				t.Fatal("missing saved match")
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			if source == SourceMnemonic {
				if !strings.Contains(string(data), "mnemonic=") || !strings.Contains(string(data), "path=m/44'/60'/0'/0/0") {
					t.Fatal("incomplete mnemonic result")
				}
			} else {
				var row logPriv
				if err := json.Unmarshal([]byte(strings.Split(string(data), "\n")[0]), &row); err != nil {
					t.Fatal(err)
				}
				if len(row.PrivateKey) != 66 || len(row.Address) != 42 {
					t.Fatal("incomplete private-key result")
				}
			}
		})
	}
}

func TestGeneratorRejectsInvalidOptions(t *testing.T) {
	for _, opt := range []Options{{Source: SourcePrivKey, Encrypt: true}, {Source: SourceMnemonic, WordsStrength: 1}} {
		if err := Run(context.Background(), opt); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
}

func TestFailedSaveStopsGenerationAndReturnsError(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "patterns.yaml")
	if err := os.WriteFile(path, []byte("symbols: ABCDEF0123456789\nregexp:\n  - pattern: '^0x'\n    final: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []Source{SourcePrivKey, SourceMnemonic} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		want := errors.New("disk full")
		err := run(ctx, Options{Source: source, PatternsPath: path, LogsBase: base, Workers: 2, CaseMaskedOut: true, DeriveN: 1}, func(string, foundEvent) error { return want })
		cancel()
		if !errors.Is(err, want) {
			t.Fatalf("source %s: lost save error: %v", source, err)
		}
	}
}

func TestGPUGenerationReportsMissingHelper(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "patterns.yaml")
	if err := os.WriteFile(path, []byte("symbols: ABCDEF0123456789\nspecific:\n  - prefix: '00'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), Options{
		Source: SourcePrivKey, PatternsPath: path, LogsBase: base, Workers: 1,
		GPUEnabled: true, CUDAExecutable: filepath.Join(base, "missing-cuda-helper"), CUDABatchSize: 256,
	})
	if err == nil || !strings.Contains(err.Error(), "start CUDA worker") {
		t.Fatalf("unexpected error: %v", err)
	}
}
