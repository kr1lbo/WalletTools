package appcfg

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Language             string `yaml:"language"`  // "ru" | "en"
	LogLevel             string `yaml:"log_level"` // "debug"|"info"|"warn"|"error"
	HideSecretsInConsole bool   `yaml:"hide_secrets_in_console"`
	Cores                int    `yaml:"cores"`
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open app config %q: %w", path, err)
	}
	defer f.Close()

	var c Config
	if err := yaml.NewDecoder(f).Decode(&c); err != nil {
		return nil, fmt.Errorf("decode app yaml %q: %w", path, err)
	}

	// defaults
	if c.Language == "" {
		c.Language = "ru"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	return &c, nil
}
