// Package subscription parses and provides access to agent subscription configs.
// Each subscription tells the Memory Compiler which events to pull, which LLM
// to use, and where to write the output.
package subscription

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"

	"github.com/opencontext/opencontext/internal/summarizer"
	"github.com/opencontext/opencontext/pkg/event"
)

// MemoryBackendType identifies the memory output backend.
type MemoryBackendType string

const (
	// BackendFile writes LLM-summarized memory.md (requires Summarizer config).
	BackendFile MemoryBackendType = "file"

	// BackendRawDump writes recent raw events directly as markdown.
	// Zero-config: no LLM needed. The reading agent interprets events itself.
	BackendRawDump MemoryBackendType = "raw_dump"
)

// Filter defines which events a subscription includes.
type Filter struct {
	Projects       []string               `mapstructure:"projects"`        // empty = all
	Sources        []event.Source         `mapstructure:"sources"`         // empty = all
	MaxSensitivity event.SensitivityLevel `mapstructure:"max_sensitivity"` // 0 defaults to 2
}

// MemoryConfig defines where compiled memory is written.
type MemoryConfig struct {
	Backend MemoryBackendType `mapstructure:"backend"`
	Path    string            `mapstructure:"path"` // for backend=file
}

// LLMConfig defines optional LLM summarization for this subscription.
type LLMConfig struct {
	Provider string `mapstructure:"provider"`
	Model    string `mapstructure:"model"`
	APIKey   string `mapstructure:"api_key"`
	BaseURL  string `mapstructure:"base_url"`
}

// Subscription is a single named compile job configuration.
type Subscription struct {
	Name            string       `mapstructure:"name"`
	Filter          Filter       `mapstructure:"filter"`
	Memory          MemoryConfig `mapstructure:"memory"`
	Schedule        string       `mapstructure:"schedule"`         // cron expression (for LLM compile)
	RefreshInterval int          `mapstructure:"refresh_interval"` // seconds; for raw_dump auto-refresh (default 30)
	LLM             *LLMConfig   `mapstructure:"llm"`
}

// EffectiveRefreshInterval returns the refresh interval in seconds, defaulting to 30.
func (s *Subscription) EffectiveRefreshInterval() time.Duration {
	if s.RefreshInterval <= 0 {
		return 30 * time.Second
	}
	return time.Duration(s.RefreshInterval) * time.Second
}

// MaxSensitivity returns the effective max sensitivity (defaults to L2).
func (s *Subscription) MaxSensitivity() event.SensitivityLevel {
	if s.Filter.MaxSensitivity == 0 {
		return event.SensitivityL2
	}
	return s.Filter.MaxSensitivity
}

// LLMSummarizerConfig converts the subscription LLM config to the summarizer
// package format. Returns nil if no LLM is configured.
func (s *Subscription) LLMSummarizerConfig() *summarizer.LLMConfig {
	if s.LLM == nil || s.LLM.Provider == "" {
		return nil
	}
	return &summarizer.LLMConfig{
		Provider: summarizer.LLMProvider(s.LLM.Provider),
		Model:    s.LLM.Model,
		APIKey:   s.LLM.APIKey,
		BaseURL:  s.LLM.BaseURL,
	}
}

// ── Config ────────────────────────────────────────────────────────────────────

// Config is the root configuration structure for contextd.
type Config struct {
	DataDir       string         `mapstructure:"data_dir"`
	ListenAddr    string         `mapstructure:"listen_addr"`
	LogLevel      string         `mapstructure:"log_level"`
	Subscriptions []Subscription `mapstructure:"subscriptions"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		DataDir:    filepath.Join(home, ".opencontext"),
		ListenAddr: "127.0.0.1:6060",
		LogLevel:   "info",
	}
}

// DBPath returns the path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "events.db")
}

// Load reads configuration from the given file path.
// Missing fields are filled with defaults.
func Load(path string) (*Config, error) {
	v := viper.New()

	// Set defaults
	defaults := DefaultConfig()
	v.SetDefault("data_dir", defaults.DataDir)
	v.SetDefault("listen_addr", defaults.ListenAddr)
	v.SetDefault("log_level", defaults.LogLevel)

	if path != "" {
		v.SetConfigFile(path)
	} else {
		home, _ := os.UserHomeDir()
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(filepath.Join(home, ".opencontext"))
		v.AddConfigPath(".")
	}

	v.SetEnvPrefix("OC")
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		// Config file missing is not an error — use defaults
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// Expand ~ in paths
	cfg.DataDir = expandHome(cfg.DataDir)
	for i := range cfg.Subscriptions {
		cfg.Subscriptions[i].Memory.Path = expandHome(cfg.Subscriptions[i].Memory.Path)
	}

	return &cfg, nil
}

func expandHome(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
