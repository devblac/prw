package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devblac/prw/internal/config"
)

// captureStdout captures stdout output from a function
func captureStdout(fn func() error) (string, error) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	errCh := make(chan error, 1)
	var buf bytes.Buffer
	go func() {
		defer w.Close()
		err := fn()
		errCh <- err
	}()

	io.Copy(&buf, r)
	os.Stdout = oldStdout

	err := <-errCh
	return buf.String(), err
}

func TestOutputJSONList(t *testing.T) {
	tests := []struct {
		name     string
		prs      []config.WatchedPR
		wantJSON string
	}{
		{
			name:     "empty list",
			prs:      []config.WatchedPR{},
			wantJSON: "[]\n",
		},
		{
			name: "single PR with all fields",
			prs: []config.WatchedPR{
				{
					Owner:          "owner",
					Repo:           "repo",
					Number:         123,
					LastKnownState: "success",
					LastChecked:    time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
					Title:          "Test PR",
				},
			},
			wantJSON: `[
  {
    "owner": "owner",
    "repo": "repo",
    "number": 123,
    "status": "success",
    "last_checked": "2025-01-15T10:30:00Z",
    "title": "Test PR"
  }
]
`,
		},
		{
			name: "PR with unknown status",
			prs: []config.WatchedPR{
				{
					Owner:          "owner",
					Repo:           "repo",
					Number:         456,
					LastKnownState: "",
					Title:          "Another PR",
				},
			},
			wantJSON: `[
  {
    "owner": "owner",
    "repo": "repo",
    "number": 456,
    "status": "unknown",
    "title": "Another PR"
  }
]
`,
		},
		{
			name: "PR without title",
			prs: []config.WatchedPR{
				{
					Owner:          "owner",
					Repo:           "repo",
					Number:         789,
					LastKnownState: "pending",
					LastChecked:    time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
				},
			},
			wantJSON: `[
  {
    "owner": "owner",
    "repo": "repo",
    "number": 789,
    "status": "pending",
    "last_checked": "2025-01-15T10:30:00Z"
  }
]
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := captureStdout(func() error {
				return outputJSONList(tt.prs)
			})
			if err != nil {
				t.Fatalf("outputJSONList() error = %v", err)
			}

			if got != tt.wantJSON {
				t.Errorf("outputJSONList() = %q, want %q", got, tt.wantJSON)
			}

			// Verify it's valid JSON
			var decoded []listPROutput
			if err := json.Unmarshal([]byte(got), &decoded); err != nil {
				t.Errorf("output is not valid JSON: %v", err)
			}
		})
	}
}

func TestListCmd_JSONOutput(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".prw", "config.json")

	oldConfigPath := config.ConfigPath
	defer func() { config.ConfigPath = oldConfigPath }()
	config.ConfigPath = func() (string, error) {
		return configPath, nil
	}

	cfg := &config.Config{
		WatchedPRs: []config.WatchedPR{
			{
				Owner:          "owner",
				Repo:           "repo",
				Number:         123,
				LastKnownState: "success",
				Title:          "Test PR",
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("failed to save test config: %v", err)
	}

	listJSON = true
	defer func() { listJSON = false }()

	output, err := captureStdout(func() error {
		return listCmd.RunE(listCmd, []string{})
	})
	if err != nil {
		t.Fatalf("listCmd.RunE() error = %v", err)
	}
	if !strings.Contains(output, `"owner"`) || !strings.Contains(output, `"repo"`) {
		t.Errorf("JSON output missing expected fields: %s", output)
	}

	var decoded []listPROutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Errorf("output is not valid JSON: %v", err)
	}

	if len(decoded) != 1 {
		t.Errorf("expected 1 PR in output, got %d", len(decoded))
	}
}

func TestListCmd_EmptyList(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".prw", "config.json")

	oldConfigPath := config.ConfigPath
	defer func() { config.ConfigPath = oldConfigPath }()
	config.ConfigPath = func() (string, error) {
		return configPath, nil
	}

	cfg := config.DefaultConfig()
	if err := cfg.Save(); err != nil {
		t.Fatalf("failed to save test config: %v", err)
	}

	listJSON = false
	output, err := captureStdout(func() error {
		return listCmd.RunE(listCmd, []string{})
	})
	if err != nil {
		t.Fatalf("listCmd.RunE() error = %v", err)
	}
	if !strings.Contains(output, "No PRs being watched") {
		t.Errorf("expected 'No PRs being watched' message, got: %s", output)
	}
}

func TestVersionCmd(t *testing.T) {
	output, _ := captureStdout(func() error {
		versionCmd.Run(versionCmd, []string{})
		return nil
	})
	if !strings.Contains(output, "prw version") {
		t.Errorf("version output missing 'prw version': %s", output)
	}
}

func TestConfigShowCmd(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".prw", "config.json")

	oldConfigPath := config.ConfigPath
	defer func() { config.ConfigPath = oldConfigPath }()
	config.ConfigPath = func() (string, error) {
		return configPath, nil
	}

	cfg := &config.Config{
		PollIntervalSeconds: 30,
		WebhookURL:          "https://example.com/webhook",
		NotificationFilter: "fail",
		WatchedPRs: []config.WatchedPR{
			{Owner: "owner", Repo: "repo", Number: 123},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("failed to save test config: %v", err)
	}

	output, err := captureStdout(func() error {
		return configShowCmd.RunE(configShowCmd, []string{})
	})
	if err != nil {
		t.Fatalf("configShowCmd.RunE() error = %v", err)
	}
	if !strings.Contains(output, "poll_interval_seconds: 30") {
		t.Errorf("output missing poll_interval_seconds: %s", output)
	}
	if !strings.Contains(output, "webhook_url: https://example.com/webhook") {
		t.Errorf("output missing webhook_url: %s", output)
	}
	if !strings.Contains(output, "notification_filter: fail") {
		t.Errorf("output missing notification_filter: %s", output)
	}
	if !strings.Contains(output, "Watched PRs: 1") {
		t.Errorf("output missing watched PRs count: %s", output)
	}
}

func TestConfigSetCmd(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".prw", "config.json")

	oldConfigPath := config.ConfigPath
	defer func() { config.ConfigPath = oldConfigPath }()
	config.ConfigPath = func() (string, error) {
		return configPath, nil
	}

	cfg := config.DefaultConfig()
	if err := cfg.Save(); err != nil {
		t.Fatalf("failed to save test config: %v", err)
	}

	tests := []struct {
		name      string
		key       string
		value     string
		wantErr   bool
		checkFunc func(*config.Config) error
	}{
		{
			name:    "set poll_interval_seconds",
			key:     "poll_interval_seconds",
			value:   "30",
			wantErr: false,
			checkFunc: func(cfg *config.Config) error {
				if cfg.PollIntervalSeconds != 30 {
					return fmt.Errorf("expected 30, got %d", cfg.PollIntervalSeconds)
				}
				return nil
			},
		},
		{
			name:    "set webhook_url",
			key:     "webhook_url",
			value:   "https://example.com/webhook",
			wantErr: false,
			checkFunc: func(cfg *config.Config) error {
				if cfg.WebhookURL != "https://example.com/webhook" {
					return fmt.Errorf("expected webhook URL, got %s", cfg.WebhookURL)
				}
				return nil
			},
		},
		{
			name:    "set github_token",
			key:     "github_token",
			value:   "test-token",
			wantErr: false,
			checkFunc: func(cfg *config.Config) error {
				if cfg.GitHubToken != "test-token" {
					return fmt.Errorf("expected token, got %s", cfg.GitHubToken)
				}
				return nil
			},
		},
		{
			name:    "set notification_filter",
			key:     "notification_filter",
			value:   "success",
			wantErr: false,
			checkFunc: func(cfg *config.Config) error {
				if cfg.NotificationFilter != "success" {
					return fmt.Errorf("expected success, got %s", cfg.NotificationFilter)
				}
				return nil
			},
		},
		{
			name:    "invalid poll_interval_seconds",
			key:     "poll_interval_seconds",
			value:   "-1",
			wantErr: true,
		},
		{
			name:    "invalid poll_interval_seconds non-numeric",
			key:     "poll_interval_seconds",
			value:   "not-a-number",
			wantErr: true,
		},
		{
			name:    "unknown key",
			key:     "unknown_key",
			value:   "value",
			wantErr: true,
		},
		{
			name:    "invalid notification_filter gets normalized",
			key:     "notification_filter",
			value:   "invalid",
			wantErr: false, // NormalizeNotificationFilter defaults invalid values to "change"
			checkFunc: func(cfg *config.Config) error {
				if cfg.NotificationFilter != "change" {
					return fmt.Errorf("expected change (normalized), got %s", cfg.NotificationFilter)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset config before each test
			cfg := config.DefaultConfig()
			if err := cfg.Save(); err != nil {
				t.Fatalf("failed to save test config: %v", err)
			}

			err := configSetCmd.RunE(configSetCmd, []string{tt.key, tt.value})
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("configSetCmd.RunE() error = %v", err)
			}

			// Reload config and verify
			loaded, err := config.Load()
			if err != nil {
				t.Fatalf("failed to reload config: %v", err)
			}

			if tt.checkFunc != nil {
				if err := tt.checkFunc(loaded); err != nil {
					t.Errorf("checkFunc failed: %v", err)
				}
			}
		})
	}
}

func TestConfigUnsetCmd(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".prw", "config.json")

	oldConfigPath := config.ConfigPath
	defer func() { config.ConfigPath = oldConfigPath }()
	config.ConfigPath = func() (string, error) {
		return configPath, nil
	}

	tests := []struct {
		name      string
		key       string
		setupFunc func(*config.Config)
		wantErr   bool
		checkFunc func(*config.Config) error
	}{
		{
			name: "unset poll_interval_seconds",
			key:  "poll_interval_seconds",
			setupFunc: func(cfg *config.Config) {
				cfg.PollIntervalSeconds = 30
			},
			wantErr: false,
			checkFunc: func(cfg *config.Config) error {
				if cfg.PollIntervalSeconds != 20 {
					return fmt.Errorf("expected default 20, got %d", cfg.PollIntervalSeconds)
				}
				return nil
			},
		},
		{
			name: "unset webhook_url",
			key:  "webhook_url",
			setupFunc: func(cfg *config.Config) {
				cfg.WebhookURL = "https://example.com/webhook"
			},
			wantErr: false,
			checkFunc: func(cfg *config.Config) error {
				if cfg.WebhookURL != "" {
					return fmt.Errorf("expected empty, got %s", cfg.WebhookURL)
				}
				return nil
			},
		},
		{
			name: "unset github_token",
			key:  "github_token",
			setupFunc: func(cfg *config.Config) {
				cfg.GitHubToken = "test-token"
			},
			wantErr: false,
			checkFunc: func(cfg *config.Config) error {
				if cfg.GitHubToken != "" {
					return fmt.Errorf("expected empty, got %s", cfg.GitHubToken)
				}
				return nil
			},
		},
		{
			name: "unset notification_filter",
			key:  "notification_filter",
			setupFunc: func(cfg *config.Config) {
				cfg.NotificationFilter = "fail"
			},
			wantErr: false,
			checkFunc: func(cfg *config.Config) error {
				if cfg.NotificationFilter != config.NotificationFilterChange {
					return fmt.Errorf("expected change, got %s", cfg.NotificationFilter)
				}
				return nil
			},
		},
		{
			name:    "unknown key",
			key:     "unknown_key",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup config
			cfg := config.DefaultConfig()
			if tt.setupFunc != nil {
				tt.setupFunc(cfg)
			}
			if err := cfg.Save(); err != nil {
				t.Fatalf("failed to save test config: %v", err)
			}

			err := configUnsetCmd.RunE(configUnsetCmd, []string{tt.key})
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("configUnsetCmd.RunE() error = %v", err)
			}

			// Reload config and verify
			loaded, err := config.Load()
			if err != nil {
				t.Fatalf("failed to reload config: %v", err)
			}

			if tt.checkFunc != nil {
				if err := tt.checkFunc(loaded); err != nil {
					t.Errorf("checkFunc failed: %v", err)
				}
			}
		})
	}
}

func TestWatchCmd_InvalidURL(t *testing.T) {
	err := watchCmd.RunE(watchCmd, []string{"invalid-url"})
	if err == nil {
		t.Error("expected error for invalid URL, got nil")
	}
	if !strings.Contains(err.Error(), "invalid PR URL") {
		t.Errorf("error message should mention invalid PR URL: %v", err)
	}
}

func TestWatchCmd_MissingToken(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".prw", "config.json")

	oldConfigPath := config.ConfigPath
	defer func() { config.ConfigPath = oldConfigPath }()
	config.ConfigPath = func() (string, error) {
		return configPath, nil
	}

	// Clear environment
	oldToken := os.Getenv("GITHUB_TOKEN")
	os.Unsetenv("GITHUB_TOKEN")
	defer func() {
		if oldToken != "" {
			os.Setenv("GITHUB_TOKEN", oldToken)
		}
	}()

	cfg := config.DefaultConfig()
	if err := cfg.Save(); err != nil {
		t.Fatalf("failed to save test config: %v", err)
	}

	err := watchCmd.RunE(watchCmd, []string{"https://github.com/owner/repo/pull/123"})
	if err == nil {
		t.Error("expected error for missing token, got nil")
	}
	if !strings.Contains(err.Error(), "missing GITHUB_TOKEN") {
		t.Errorf("error message should mention missing GITHUB_TOKEN: %v", err)
	}
}

func TestUnwatchCmd_InvalidURL(t *testing.T) {
	err := unwatchCmd.RunE(unwatchCmd, []string{"invalid-url"})
	if err == nil {
		t.Error("expected error for invalid URL, got nil")
	}
	if !strings.Contains(err.Error(), "invalid PR URL") {
		t.Errorf("error message should mention invalid PR URL: %v", err)
	}
}

func TestRunCmd_InvalidNotificationFilter(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".prw", "config.json")

	oldConfigPath := config.ConfigPath
	defer func() { config.ConfigPath = oldConfigPath }()
	config.ConfigPath = func() (string, error) {
		return configPath, nil
	}

	cfg := config.DefaultConfig()
	cfg.GitHubToken = "test-token"
	if err := cfg.Save(); err != nil {
		t.Fatalf("failed to save test config: %v", err)
	}

	notifyFilter = "invalid-filter"
	defer func() { notifyFilter = "" }()

	// The current implementation normalizes invalid filters to "change",
	// so it won't error. The watcher will run but exit early if no PRs are watched.
	// This test verifies the command doesn't crash.
	err := runCmd.RunE(runCmd, []string{})
	// Should not error - invalid filter gets normalized to "change"
	if err != nil && !strings.Contains(err.Error(), "No PRs being watched") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestListPROutput_JSONMarshaling(t *testing.T) {
	now := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	output := listPROutput{
		Owner:       "owner",
		Repo:        "repo",
		Number:      123,
		Status:      "success",
		LastChecked: &now,
		Title:       "Test PR",
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded listPROutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Owner != output.Owner || decoded.Repo != output.Repo || decoded.Number != output.Number {
		t.Errorf("decoded output doesn't match: %+v", decoded)
	}
}

func TestListPROutput_WithoutOptionalFields(t *testing.T) {
	output := listPROutput{
		Owner:  "owner",
		Repo:   "repo",
		Number: 123,
		Status: "unknown",
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded listPROutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.LastChecked != nil {
		t.Errorf("expected nil LastChecked, got %v", decoded.LastChecked)
	}
	if decoded.Title != "" {
		t.Errorf("expected empty Title, got %q", decoded.Title)
	}
}

