package config

import "testing"

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
