package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	NotificationFilterChange  = "change"
	NotificationFilterFail    = "fail"
	NotificationFilterSuccess = "success"
)

// Config represents the application configuration and state.
type Config struct {
	// Global settings
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	WebhookURL          string `json:"webhook_url,omitempty"`
	GitHubToken         string `json:"github_token,omitempty"`
	NotificationFilter  string `json:"notification_filter,omitempty"`

	// Watched PRs
	WatchedPRs []WatchedPR `json:"watched_prs"`
}

// WatchedPR represents a pull request being watched.
type WatchedPR struct {
	Owner          string    `json:"owner"`
	Repo           string    `json:"repo"`
	Number         int       `json:"number"`
	LastKnownSHA   string    `json:"last_known_sha,omitempty"`
	LastKnownState string    `json:"last_known_state,omitempty"`
	LastChecked    time.Time `json:"last_checked,omitempty"`
	Title          string    `json:"title,omitempty"`
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		PollIntervalSeconds: 20,
		NotificationFilter:  NotificationFilterChange,
		WatchedPRs:          []WatchedPR{},
	}
}

// ConfigPath is a variable that returns the path to the config file.
// It's a variable so tests can override it.
var ConfigPath = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}
	return filepath.Join(home, ".prw", "config.json"), nil
}

// Load reads the config from disk. If the file doesn't exist, returns default config.
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Apply defaults for missing fields
	if cfg.PollIntervalSeconds == 0 {
		cfg.PollIntervalSeconds = 20
	}
	if cfg.WatchedPRs == nil {
		cfg.WatchedPRs = []WatchedPR{}
	}
	cfg.NotificationFilter = normalizeNotificationFilter(cfg.NotificationFilter)

	return &cfg, nil
}

// Save writes the config to disk.
func (c *Config) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// AddPR adds a PR to the watched list if not already present.
func (c *Config) AddPR(pr WatchedPR) bool {
	for _, existing := range c.WatchedPRs {
		if existing.Owner == pr.Owner && existing.Repo == pr.Repo && existing.Number == pr.Number {
			return false
		}
	}
	c.WatchedPRs = append(c.WatchedPRs, pr)
	return true
}

// RemovePR removes a PR from the watched list.
func (c *Config) RemovePR(owner, repo string, number int) bool {
	for i, pr := range c.WatchedPRs {
		if pr.Owner == owner && pr.Repo == repo && pr.Number == number {
			c.WatchedPRs = append(c.WatchedPRs[:i], c.WatchedPRs[i+1:]...)
			return true
		}
	}
	return false
}

// UpdatePR updates the state of a watched PR.
func (c *Config) UpdatePR(owner, repo string, number int, sha, state string) {
	for i := range c.WatchedPRs {
		if c.WatchedPRs[i].Owner == owner && c.WatchedPRs[i].Repo == repo && c.WatchedPRs[i].Number == number {
			c.WatchedPRs[i].LastKnownSHA = sha
			c.WatchedPRs[i].LastKnownState = state
			c.WatchedPRs[i].LastChecked = time.Now()
			return
		}
	}
}

// GetToken returns the GitHub token from config or environment.
func (c *Config) GetToken() string {
	if c.GitHubToken != "" {
		return c.GitHubToken
	}
	return os.Getenv("GITHUB_TOKEN")
}

// normalizeNotificationFilter applies defaults and validation for the notification filter.
func normalizeNotificationFilter(value string) string {
	filter := strings.ToLower(strings.TrimSpace(value))
	switch filter {
	case NotificationFilterFail, NotificationFilterSuccess, NotificationFilterChange:
		return filter
	default:
		return NotificationFilterChange
	}
}

// IsValidNotificationFilter reports whether the provided filter is allowed.
func IsValidNotificationFilter(value string) bool {
	filter := strings.ToLower(strings.TrimSpace(value))
	return filter == NotificationFilterFail || filter == NotificationFilterSuccess || filter == NotificationFilterChange
}

// NormalizeNotificationFilter sanitizes user input and applies defaults.
func NormalizeNotificationFilter(value string) string {
	return normalizeNotificationFilter(value)
}
