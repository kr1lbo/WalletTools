package patterns

import (
	"strings"
	"testing"

	"WalletTools/pkg/config"
)

func TestSpecificMatchesAddressBody(t *testing.T) {
	cfg := &config.PatternsConfig{
		Symbols:  "A B C D E F 0 1 2 3 4 5 6 7 8 9",
		Specific: []config.SpecificPattern{{Prefix: "beef", Suffix: "0000", Final: true}},
	}
	addr := "0xbeef" + strings.Repeat("1", 32) + "0000"

	got := MatchAddress(cfg, addr)
	if got == nil || got.Kind != "specific" || !got.Final {
		t.Fatalf("expected specific final match, got %#v", got)
	}
}

func TestEdgesMatchAddressBodyPrefix(t *testing.T) {
	cfg := &config.PatternsConfig{
		Symbols: "A B C D E F 0 1 2 3 4 5 6 7 8 9",
		Edges:   config.EdgeConfig{MinCount: 4, Side: "prefix"},
	}
	addr := "0x0000" + strings.Repeat("a", 36)

	got := MatchAddress(cfg, addr)
	if got == nil || got.Kind != "edges" {
		t.Fatalf("expected edges match, got %#v", got)
	}
}

func TestSymmetricSupportsPlaceholdersAndLiterals(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.PatternsConfig
		addr string
	}{
		{
			name: "placeholders",
			cfg: &config.PatternsConfig{
				Symbols:   "A B C D E F 0 1 2 3 4 5 6 7 8 9",
				Symmetric: []config.SymmetricPattern{{Prefix: "XX", Suffix: "YY"}},
			},
			addr: "0xaa" + strings.Repeat("1", 36) + "bb",
		},
		{
			name: "literal prefix and suffix",
			cfg: &config.PatternsConfig{
				Symbols:   "A B C D E F 0 1 2 3 4 5 6 7 8 9",
				Symmetric: []config.SymmetricPattern{{Prefix: "1234", Suffix: "4321"}},
			},
			addr: "0x1234" + strings.Repeat("a", 32) + "4321",
		},
		{
			name: "shared placeholder binding",
			cfg: &config.PatternsConfig{
				Symbols:   "A B C D E F 0 1 2 3 4 5 6 7 8 9",
				Symmetric: []config.SymmetricPattern{{Prefix: "XY", Suffix: "YX"}},
			},
			addr: "0xab" + strings.Repeat("1", 36) + "ba",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchAddress(tt.cfg, tt.addr)
			if got == nil || got.Kind != "symmetric" {
				t.Fatalf("expected symmetric match, got %#v", got)
			}
		})
	}
}

func TestRegexpMatchesFullAddress(t *testing.T) {
	cfg := &config.PatternsConfig{
		Symbols: "A B C D E F 0 1 2 3 4 5 6 7 8 9",
		Regexp:  []config.RegexpPattern{{Pattern: "^0xface"}},
	}
	addr := "0xface" + strings.Repeat("0", 36)

	got := MatchAddress(cfg, addr)
	if got == nil || got.Kind != "regexp" {
		t.Fatalf("expected regexp match, got %#v", got)
	}
}
