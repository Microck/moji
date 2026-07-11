package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/microck/moji/internal/provider"
	"github.com/microck/moji/internal/rank"
	"gopkg.in/yaml.v3"
)

type ProviderConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Instance string `yaml:"instance,omitempty"`
}

type Config struct {
	DownloadDir          string                     `yaml:"download_dir"`
	GitHubToken          string                     `yaml:"github_token"`
	SearchTimeoutSeconds int                        `yaml:"search_timeout_seconds"`
	CacheTTLSeconds      int                        `yaml:"cache_ttl_seconds"`
	DefaultFormats       []string                   `yaml:"default_formats"`
	Ranking              rank.Weights               `yaml:"ranking"`
	RateLimits           map[string]RateLimitConfig `yaml:"rate_limits"`
	Providers            map[string]ProviderConfig  `yaml:"providers"`
	SourcePlugins        []string                   `yaml:"source_plugins,omitempty"`
}

type RateLimitConfig struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
	Retries        int `yaml:"retries"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		DownloadDir:          filepath.Join(home, "Downloads", "moji"),
		SearchTimeoutSeconds: 15, CacheTTLSeconds: 3600,
		DefaultFormats: []string{"otf", "ttf", "woff2", "dfont", "pfb", "pfm"}, Ranking: rank.DefaultWeights(),
		RateLimits: map[string]RateLimitConfig{
			"github":    {TimeoutSeconds: 15, Retries: 2},
			"getfonts":  {TimeoutSeconds: 15, Retries: 1},
			"registry":  {TimeoutSeconds: 15, Retries: 1},
			"plugins":   {TimeoutSeconds: 20, Retries: 0},
			"websearch": {TimeoutSeconds: 20, Retries: 0},
		},
		Providers: map[string]ProviderConfig{
			"github": {Enabled: true}, "getfonts": {Enabled: true},
			"registry": {Enabled: true}, "plugins": {Enabled: true}, "websearch": {Enabled: true},
		},
	}
}

func Path() (string, error) {
	if override := os.Getenv("MOJI_CONFIG"); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".moji", "config.yaml"), nil
}

func Load(path string) (Config, error) {
	config := Default()
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("couldn't read config at %s: %w. Check that the file is readable", path, err)
	}
	if err := yaml.Unmarshal(content, &config); err != nil {
		return Config{}, fmt.Errorf("couldn't parse config at %s: %w. Fix the YAML or move the file aside to use defaults", path, err)
	}
	if config.SearchTimeoutSeconds <= 0 {
		return Config{}, errors.New("search_timeout_seconds must be greater than 0 in the config file")
	}
	if config.CacheTTLSeconds < 0 {
		return Config{}, errors.New("cache_ttl_seconds must be 0 or greater in the config file")
	}
	formats, err := ParseFormats(strings.Join(config.DefaultFormats, ","))
	if err != nil {
		return Config{}, fmt.Errorf("default_formats: %w", err)
	}
	config.DefaultFormats = formats
	return config, nil
}

func Save(path string, config Config) error {
	content, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("couldn't prepare the config for saving: %w. The existing config was not changed", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("couldn't create the config directory for %s: %w. Check the directory permissions, then try again", path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("couldn't create a temporary config beside %s: %w. The existing config was not changed", path, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("couldn't secure the temporary config file: %w. The existing config was not changed", err)
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return fmt.Errorf("couldn't write the temporary config file: %w. The existing config was not changed", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("couldn't finish writing the temporary config file: %w. The existing config was not changed", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("couldn't replace %s with the new config: %w. The existing config was not changed", path, err)
	}
	return nil
}

func ParseFormats(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" || strings.EqualFold(value, "all") {
		return []string{"otf", "ttf", "woff", "woff2", "dfont", "pfb", "pfm"}, nil
	}
	seen := make(map[string]bool)
	formats := make([]string, 0, 4)
	for _, raw := range strings.Split(value, ",") {
		format := strings.ToLower(strings.TrimSpace(raw))
		if format != "otf" && format != "ttf" && format != "woff" && format != "woff2" && format != "dfont" && format != "pfb" && format != "pfm" {
			return nil, fmt.Errorf("unsupported format %q (choose otf, ttf, woff, woff2, dfont, pfb, or pfm)", raw)
		}
		if !seen[format] {
			seen[format] = true
			formats = append(formats, format)
		}
	}
	return formats, nil
}

func (config Config) Policies() map[string]provider.RatePolicy {
	policies := make(map[string]provider.RatePolicy, len(config.RateLimits))
	for name, value := range config.RateLimits {
		policies[name] = provider.RatePolicy{Timeout: time.Duration(value.TimeoutSeconds) * time.Second, Retries: value.Retries, BackoffBase: 500 * time.Millisecond, BackoffJitter: 250 * time.Millisecond}
	}
	return policies
}

func (config Config) Token() string {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	return config.GitHubToken
}
