package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.PollIntervalSeconds != 20 {
		t.Errorf("expected default poll interval 20, got %d", cfg.PollIntervalSeconds)
	}
	if cfg.WatchedPRs == nil {
		t.Error("expected WatchedPRs to be initialized")
	}
	if len(cfg.WatchedPRs) != 0 {
		t.Errorf("expected empty WatchedPRs, got %d items", len(cfg.WatchedPRs))
	}
}

func TestConfigSaveAndLoad(t *testing.T) {
	// Use a temporary directory for testing
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".prw", "config.json")

	// Override ConfigPath for testing
	oldConfigPath := ConfigPath
	defer func() { ConfigPath = oldConfigPath }()
	ConfigPath = func() (string, error) {
		return configPath, nil
	}

	// Create a config
	cfg := &Config{
		PollIntervalSeconds: 30,
		WebhookURL:          "https://example.com/webhook",
		GitHubToken:         "test-token",
		WatchedPRs: []WatchedPR{
			{
				Owner:          "owner",
				Repo:           "repo",
				Number:         123,
				LastKnownSHA:   "abc123",
				LastKnownState: "success",
				Title:          "Test PR",
			},
		},
	}

	// Save it
	if err := cfg.Save(); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// Load it back
	loaded, err := Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Verify fields
	if loaded.PollIntervalSeconds != 30 {
		t.Errorf("expected PollIntervalSeconds 30, got %d", loaded.PollIntervalSeconds)
	}
	if loaded.WebhookURL != "https://example.com/webhook" {
		t.Errorf("expected WebhookURL to match, got %s", loaded.WebhookURL)
	}
	if loaded.GitHubToken != "test-token" {
		t.Errorf("expected GitHubToken to match, got %s", loaded.GitHubToken)
	}
	if len(loaded.WatchedPRs) != 1 {
		t.Fatalf("expected 1 watched PR, got %d", len(loaded.WatchedPRs))
	}

	pr := loaded.WatchedPRs[0]
	if pr.Owner != "owner" || pr.Repo != "repo" || pr.Number != 123 {
		t.Errorf("PR details don't match: %+v", pr)
	}
}

func TestLoadNonExistentConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".prw", "config.json")

	oldConfigPath := ConfigPath
	defer func() { ConfigPath = oldConfigPath }()
	ConfigPath = func() (string, error) {
		return configPath, nil
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected Load to succeed with default config, got error: %v", err)
	}

	if cfg.PollIntervalSeconds != 20 {
		t.Errorf("expected default poll interval, got %d", cfg.PollIntervalSeconds)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".prw", "config.json")

	oldConfigPath := ConfigPath
	defer func() { ConfigPath = oldConfigPath }()
	ConfigPath = func() (string, error) {
		return configPath, nil
	}

	// Write invalid JSON
	os.MkdirAll(filepath.Dir(configPath), 0755)
	os.WriteFile(configPath, []byte("{invalid json}"), 0600)

	_, err := Load()
	if err == nil {
		t.Error("expected Load to fail with invalid JSON")
	}
}

func TestAddPR(t *testing.T) {
	cfg := DefaultConfig()

	pr1 := WatchedPR{Owner: "owner", Repo: "repo", Number: 1}
	pr2 := WatchedPR{Owner: "owner", Repo: "repo", Number: 2}
	pr1Duplicate := WatchedPR{Owner: "owner", Repo: "repo", Number: 1}

	if !cfg.AddPR(pr1) {
		t.Error("expected AddPR to return true for new PR")
	}
	if len(cfg.WatchedPRs) != 1 {
		t.Errorf("expected 1 PR, got %d", len(cfg.WatchedPRs))
	}

	if !cfg.AddPR(pr2) {
		t.Error("expected AddPR to return true for second PR")
	}
	if len(cfg.WatchedPRs) != 2 {
		t.Errorf("expected 2 PRs, got %d", len(cfg.WatchedPRs))
	}

	if cfg.AddPR(pr1Duplicate) {
		t.Error("expected AddPR to return false for duplicate PR")
	}
	if len(cfg.WatchedPRs) != 2 {
		t.Errorf("expected 2 PRs after duplicate, got %d", len(cfg.WatchedPRs))
	}
}

func TestRemovePR(t *testing.T) {
	cfg := &Config{
		WatchedPRs: []WatchedPR{
			{Owner: "owner", Repo: "repo", Number: 1},
			{Owner: "owner", Repo: "repo", Number: 2},
		},
	}

	if !cfg.RemovePR("owner", "repo", 1) {
		t.Error("expected RemovePR to return true")
	}
	if len(cfg.WatchedPRs) != 1 {
		t.Errorf("expected 1 PR after removal, got %d", len(cfg.WatchedPRs))
	}
	if cfg.WatchedPRs[0].Number != 2 {
		t.Errorf("wrong PR remaining: %+v", cfg.WatchedPRs[0])
	}

	if cfg.RemovePR("owner", "repo", 99) {
		t.Error("expected RemovePR to return false for non-existent PR")
	}
}

func TestUpdatePR(t *testing.T) {
	cfg := &Config{
		WatchedPRs: []WatchedPR{
			{Owner: "owner", Repo: "repo", Number: 1, LastKnownState: "pending"},
		},
	}

	cfg.UpdatePR("owner", "repo", 1, "newsha", "success")

	pr := cfg.WatchedPRs[0]
	if pr.LastKnownSHA != "newsha" {
		t.Errorf("expected SHA to be updated, got %s", pr.LastKnownSHA)
	}
	if pr.LastKnownState != "success" {
		t.Errorf("expected state to be updated, got %s", pr.LastKnownState)
	}
	if pr.LastChecked.IsZero() {
		t.Error("expected LastChecked to be set")
	}
}

func TestGetToken(t *testing.T) {
	// Clear environment variable
	oldToken := os.Getenv("GITHUB_TOKEN")
	os.Unsetenv("GITHUB_TOKEN")
	defer func() {
		if oldToken != "" {
			os.Setenv("GITHUB_TOKEN", oldToken)
		}
	}()

	tests := []struct {
		name        string
		configToken string
		envToken    string
		expected    string
	}{
		{
			name:        "config token takes precedence",
			configToken: "config-token",
			envToken:    "env-token",
			expected:    "config-token",
		},
		{
			name:        "env token when config is empty",
			configToken: "",
			envToken:    "env-token",
			expected:    "env-token",
		},
		{
			name:        "empty when both empty",
			configToken: "",
			envToken:    "",
			expected:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envToken != "" {
				os.Setenv("GITHUB_TOKEN", tt.envToken)
			} else {
				os.Unsetenv("GITHUB_TOKEN")
			}

			cfg := &Config{GitHubToken: tt.configToken}
			token := cfg.GetToken()
			if token != tt.expected {
				t.Errorf("expected token %q, got %q", tt.expected, token)
			}
		})
	}
}

func TestConfigJSONMarshaling(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	cfg := &Config{
		PollIntervalSeconds: 30,
		WebhookURL:          "https://example.com",
		GitHubToken:         "token",
		WatchedPRs: []WatchedPR{
			{
				Owner:          "owner",
				Repo:           "repo",
				Number:         1,
				LastKnownSHA:   "sha",
				LastKnownState: "success",
				LastChecked:    now,
				Title:          "Test",
			},
		},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded Config
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.PollIntervalSeconds != cfg.PollIntervalSeconds {
		t.Error("PollIntervalSeconds not preserved")
	}
	if decoded.WebhookURL != cfg.WebhookURL {
		t.Error("WebhookURL not preserved")
	}
	if len(decoded.WatchedPRs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(decoded.WatchedPRs))
	}
}
