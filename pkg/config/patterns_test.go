package config

import (
	"gopkg.in/yaml.v3"
	"os"
	"strings"
	"testing"
)

func TestRejectInvalidPatterns(t *testing.T) {
	for _, input := range []string{
		"regexp: [{pattern: '(a)\\1'}]",
		"regexp: [{pattern: ''}]",
		"specific: [{prefix: '0xbeef'}]",
		"specific: [{prefix: '', suffix: ''}]",
		"edges: {minCount: 41}",
	} {
		var cfg PatternsConfig
		if err := yaml.Unmarshal([]byte("symbols: ABCDEF0123456789\n"+input), &cfg); err != nil {
			t.Fatal(err)
		}
		if err := validate(&cfg); err == nil {
			t.Errorf("accepted invalid pattern: %s", input)
		}
	}
}

func TestEdgesDefaultSide(t *testing.T) {
	cfg := &PatternsConfig{Symbols: "0123456789abcdef", Edges: EdgeConfig{MinCount: 4}}
	if err := validate(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Edges.Side != "any" {
		t.Fatal("missing side should match either edge")
	}
}

func TestReadmePatternExamplesAreValid(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "```yaml\n")
	if len(blocks) < 2 {
		t.Fatal("no documented YAML examples found")
	}
	for _, block := range blocks[1:] {
		example := strings.SplitN(block, "```", 2)[0]
		if strings.Contains(example, "log_level:") {
			continue
		}
		cfg := PatternsConfig{Symbols: "0123456789abcdef"}
		if err := yaml.Unmarshal([]byte(example), &cfg); err != nil {
			t.Fatal(err)
		}
		if err := validate(&cfg); err != nil {
			t.Errorf("invalid README example: %v", err)
		}
	}
}

func TestValidateSymmetricSupportsLiteralHex(t *testing.T) {
	cfg := &PatternsConfig{
		Symbols:   "A B C D E F 0 1 2 3 4 5 6 7 8 9",
		Symmetric: []SymmetricPattern{{Prefix: "1234", Suffix: "4321"}},
	}

	if err := validate(cfg); err != nil {
		t.Fatalf("expected literal symmetric pattern to validate: %v", err)
	}
}

func TestValidateSymmetricRejectsMixedInvalidPattern(t *testing.T) {
	cfg := &PatternsConfig{
		Symbols:   "A B C D E F 0 1 2 3 4 5 6 7 8 9",
		Symmetric: []SymmetricPattern{{Prefix: "12Z4", Suffix: "4321"}},
	}

	if err := validate(cfg); err == nil {
		t.Fatal("expected invalid symmetric pattern to fail validation")
	}
}
