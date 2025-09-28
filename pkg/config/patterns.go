package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// PatternsConfig describes the configuration for finding patterns.
type PatternsConfig struct {
	Symbols       string             `yaml:"symbols"`
	CaseSensitive bool               `yaml:"case_sensitive"` // NEW: true -> учитывать регистр
	Symmetric     []SymmetricPattern `yaml:"symmetric"`
	Specific      []SpecificPattern  `yaml:"specific"`
	Edges         EdgeConfig         `yaml:"edges"`
	Regexp        []RegexpPattern    `yaml:"regexp"`
}

type SymmetricPattern struct {
	Prefix string `yaml:"prefix"`
	Suffix string `yaml:"suffix"`
	Final  bool   `yaml:"final"`
}

type SpecificPattern struct {
	Prefix string `yaml:"prefix"`
	Suffix string `yaml:"suffix"`
	Final  bool   `yaml:"final"`
}

type EdgeConfig struct {
	MinCount int    `yaml:"minCount"`
	Side     string `yaml:"side"` // any|prefix|suffix
	Final    bool   `yaml:"final"`
}

type RegexpPattern struct {
	Pattern string `yaml:"pattern"`
	Final   bool   `yaml:"final"`
}

func Load(path string) (*PatternsConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	defer f.Close()

	var cfg PatternsConfig
	dec := yaml.NewDecoder(f)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode yaml %q: %w", path, err)
	}

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("config validation %q: %w", path, err)
	}

	return &cfg, nil
}

func validate(c *PatternsConfig) error {
	if c == nil {
		return errors.New("nil config")
	}
	if c.Symbols == "" {
		return errors.New("symbols must not be empty")
	}
	if c.Edges.MinCount < 0 {
		return errors.New("edges.minCount must be >= 0")
	}
	if c.Edges.Side != "" {
		switch c.Edges.Side {
		case "any", "prefix", "suffix":
		default:
			return errors.New("edges.side must be one of: any, prefix, suffix")
		}
	}

	for i, sp := range c.Symmetric {
		if err := validateOnlyXY(sp.Prefix); err != nil {
			return fmt.Errorf("symmetric[%d].prefix: %w", i, err)
		}
		if err := validateOnlyXY(sp.Suffix); err != nil {
			return fmt.Errorf("symmetric[%d].suffix: %w", i, err)
		}
	}

	if len(c.Symmetric) == 0 && len(c.Specific) == 0 && c.Edges.MinCount == 0 && len(c.Regexp) == 0 {
		return errors.New("no patterns defined: symmetric, specific, edges, regexp are all empty")
	}

	return nil
}

func validateOnlyXY(s string) error {
	if s == "" {
		return errors.New("must be non-empty and contain only X/Y")
	}
	up := strings.ToUpper(s)
	for i := 0; i < len(up); i++ {
		if up[i] != 'X' && up[i] != 'Y' {
			return errors.New("must contain only placeholders X or Y")
		}
	}
	return nil
}
