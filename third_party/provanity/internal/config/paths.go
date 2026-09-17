package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const AppName = "provanity"

type Paths struct {
	ConfigDir      string
	CacheDir       string
	ProfilesDir    string
	StateDir       string
	LogsDir        string
	BinaryCacheDir string
}

func ResolvePaths() (Paths, error) {
	return ResolvePathsForApp(AppName)
}

func ResolvePathsForApp(app string) (Paths, error) {
	configBase, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user config dir: %w", err)
	}

	cacheBase, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user cache dir: %w", err)
	}

	return ResolvePathsFromBase(configBase, cacheBase, app)
}

func ResolvePathsFromBase(configBase, cacheBase, app string) (Paths, error) {
	if err := validateAppName(app); err != nil {
		return Paths{}, err
	}
	if configBase == "" {
		return Paths{}, fmt.Errorf("config base directory is empty")
	}
	if cacheBase == "" {
		return Paths{}, fmt.Errorf("cache base directory is empty")
	}

	configDir := filepath.Join(configBase, app)
	cacheDir := filepath.Join(cacheBase, app)
	return Paths{
		ConfigDir:      configDir,
		CacheDir:       cacheDir,
		ProfilesDir:    filepath.Join(configDir, "profiles"),
		StateDir:       filepath.Join(configDir, "state"),
		LogsDir:        filepath.Join(configDir, "logs"),
		BinaryCacheDir: filepath.Join(cacheDir, "cache"),
	}, nil
}

func (p Paths) Ensure() error {
	for _, dir := range []string{
		p.ConfigDir,
		p.CacheDir,
		p.ProfilesDir,
		p.StateDir,
		p.LogsDir,
		p.BinaryCacheDir,
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

func validateAppName(app string) error {
	if app == "" {
		return fmt.Errorf("app name is empty")
	}
	if strings.ContainsAny(app, `/\`) || app == "." || app == ".." {
		return fmt.Errorf("invalid app name %q", app)
	}
	return nil
}
