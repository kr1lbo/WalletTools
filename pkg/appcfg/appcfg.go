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

type rawConfig struct {
	Language             string `yaml:"language"`
	LogLevel             string `yaml:"log_level"`
	HideSecretsInConsole *bool  `yaml:"hide_secrets_in_console"`
	LegacyHideSecrets    *bool  `yaml:"hide_secrets"`
	Cores                int    `yaml:"cores"`
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open app config %q: %w", path, err)
	}
	defer f.Close()

	var raw rawConfig
	if err := yaml.NewDecoder(f).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode app yaml %q: %w", path, err)
	}

	c := Config{
		HideSecretsInConsole: true,
		Language:             raw.Language,
		LogLevel:             raw.LogLevel,
		Cores:                raw.Cores,
	}
	if raw.HideSecretsInConsole != nil {
		c.HideSecretsInConsole = *raw.HideSecretsInConsole
	} else if raw.LegacyHideSecrets != nil {
		c.HideSecretsInConsole = *raw.LegacyHideSecrets
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
