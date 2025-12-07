package watcher

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
	"github.com/devblac/prw/internal/notify"
)

// mockGitHubClient implements GitHubClient for testing.
type mockGitHubClient struct {
	prs      map[string]*github.PullRequest
	statuses map[string]*github.CombinedStatus
	err      error
}

func (m *mockGitHubClient) GetPullRequest(owner, repo string, number int) (*github.PullRequest, error) {
	if m.err != nil {
		return nil, m.err
	}
	key := fmt.Sprintf("%s/%s/%d", owner, repo, number)
	pr, ok := m.prs[key]
	if !ok {
		return nil, fmt.Errorf("PR not found")
	}
	return pr, nil
}

func (m *mockGitHubClient) GetCombinedStatus(owner, repo, ref string) (*github.CombinedStatus, error) {
	if m.err != nil {
		return nil, m.err
	}
	status, ok := m.statuses[ref]
	if !ok {
		return &github.CombinedStatus{State: "pending", SHA: ref}, nil
	}
	return status, nil
}

// mockNotifier implements Notifier for testing.
type mockNotifier struct {
	events []*notify.StatusChangeEvent
	err    error
}

func (m *mockNotifier) Notify(event *notify.StatusChangeEvent) error {
	if m.err != nil {
		return m.err
	}
	m.events = append(m.events, event)
	return nil
}

func TestWatcherNotificationError(t *testing.T) {
	pr := &github.PullRequest{
		Number: 1,
		Title:  "Test PR",
	}
	pr.Head.SHA = "sha123"

	client := &mockGitHubClient{
		prs: map[string]*github.PullRequest{
			"owner/repo/1": pr,
		},
		statuses: map[string]*github.CombinedStatus{
			"sha123": {State: "success", SHA: "sha123"},
		},
	}

	cfg := &config.Config{
		WatchedPRs: []config.WatchedPR{
			{
				Owner:          "owner",
				Repo:           "repo",
				Number:         1,
				LastKnownState: "pending",
			},
		},
	}

	notifier := &mockNotifier{err: fmt.Errorf("notification failed")}
	w := New(client, cfg, notifier)

	// Should not return error, just print warning
	if err := w.checkPR(&cfg.WatchedPRs[0]); err != nil {
		t.Errorf("checkPR should not fail when notification fails: %v", err)
	}
}

func TestWatcherNoStatusChange(t *testing.T) {
	pr := &github.PullRequest{
		Number: 1,
		Title:  "Test PR",
	}
	pr.Head.SHA = "sha123"

	client := &mockGitHubClient{
		prs: map[string]*github.PullRequest{
			"owner/repo/1": pr,
		},
		statuses: map[string]*github.CombinedStatus{
			"sha123": {State: "success", SHA: "sha123"},
		},
	}

	cfg := &config.Config{
		PollIntervalSeconds: 1,
		WatchedPRs: []config.WatchedPR{
			{
				Owner:          "owner",
				Repo:           "repo",
				Number:         1,
				LastKnownSHA:   "sha123",
				LastKnownState: "success",
			},
		},
	}

	notifier := &mockNotifier{}
	w := New(client, cfg, notifier)

	// Check the PR once
	if err := w.checkPR(&cfg.WatchedPRs[0]); err != nil {
		t.Fatalf("checkPR failed: %v", err)
	}

	// No notification should be sent since state didn't change
	if len(notifier.events) != 0 {
		t.Errorf("expected 0 notifications, got %d", len(notifier.events))
	}
}

func TestWatcherStatusChange(t *testing.T) {
	pr := &github.PullRequest{
		Number: 1,
		Title:  "Test PR",
	}
	pr.Head.SHA = "sha123"

	client := &mockGitHubClient{
		prs: map[string]*github.PullRequest{
			"owner/repo/1": pr,
		},
		statuses: map[string]*github.CombinedStatus{
			"sha123": {State: "success", SHA: "sha123"},
		},
	}

	cfg := &config.Config{
		PollIntervalSeconds: 1,
		WatchedPRs: []config.WatchedPR{
			{
				Owner:          "owner",
				Repo:           "repo",
				Number:         1,
				LastKnownSHA:   "sha123",
				LastKnownState: "pending", // Different from actual state
			},
		},
	}

	notifier := &mockNotifier{}
	w := New(client, cfg, notifier)

	if err := w.checkPR(&cfg.WatchedPRs[0]); err != nil {
		t.Fatalf("checkPR failed: %v", err)
	}

	// Should send a notification
	if len(notifier.events) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifier.events))
	}

	event := notifier.events[0]
	if event.PreviousState != "pending" {
		t.Errorf("expected previous state 'pending', got %q", event.PreviousState)
	}
	if event.CurrentState != "success" {
		t.Errorf("expected current state 'success', got %q", event.CurrentState)
	}
	if event.Owner != "owner" || event.Repo != "repo" || event.Number != 1 {
		t.Errorf("unexpected event details: %+v", event)
	}

	// State should be updated
	updatedPR := cfg.WatchedPRs[0]
	if updatedPR.LastKnownState != "success" {
		t.Errorf("expected state to be updated to 'success', got %q", updatedPR.LastKnownState)
	}
}

func TestWatcherFirstCheck(t *testing.T) {
	pr := &github.PullRequest{
		Number: 1,
		Title:  "Test PR",
	}
	pr.Head.SHA = "sha123"

	client := &mockGitHubClient{
		prs: map[string]*github.PullRequest{
			"owner/repo/1": pr,
		},
		statuses: map[string]*github.CombinedStatus{
			"sha123": {State: "success", SHA: "sha123"},
		},
	}

	cfg := &config.Config{
		PollIntervalSeconds: 1,
		WatchedPRs: []config.WatchedPR{
			{
				Owner:          "owner",
				Repo:           "repo",
				Number:         1,
				LastKnownState: "", // First check
			},
		},
	}

	notifier := &mockNotifier{}
	w := New(client, cfg, notifier)

	if err := w.checkPR(&cfg.WatchedPRs[0]); err != nil {
		t.Fatalf("checkPR failed: %v", err)
	}

	// No notification on first check
	if len(notifier.events) != 0 {
		t.Errorf("expected 0 notifications on first check, got %d", len(notifier.events))
	}

	// State should be recorded
	updatedPR := cfg.WatchedPRs[0]
	if updatedPR.LastKnownState != "success" {
		t.Errorf("expected state to be set to 'success', got %q", updatedPR.LastKnownState)
	}
}

func TestWatcherGitHubError(t *testing.T) {
	client := &mockGitHubClient{
		err: fmt.Errorf("API error"),
	}

	cfg := &config.Config{
		WatchedPRs: []config.WatchedPR{
			{Owner: "owner", Repo: "repo", Number: 1},
		},
	}

	notifier := &mockNotifier{}
	w := New(client, cfg, notifier)

	err := w.checkPR(&cfg.WatchedPRs[0])
	if err == nil {
		t.Error("expected error when GitHub API fails")
	}
}

func TestWatcherRunContext(t *testing.T) {
	pr := &github.PullRequest{
		Number: 1,
		Title:  "Test PR",
	}
	pr.Head.SHA = "sha123"

	client := &mockGitHubClient{
		prs: map[string]*github.PullRequest{
			"owner/repo/1": pr,
		},
		statuses: map[string]*github.CombinedStatus{
			"sha123": {State: "success", SHA: "sha123"},
		},
	}

	cfg := &config.Config{
		PollIntervalSeconds: 1,
		WatchedPRs: []config.WatchedPR{
			{
				Owner:          "owner",
				Repo:           "repo",
				Number:         1,
				LastKnownState: "success",
			},
		},
	}

	notifier := &mockNotifier{}
	w := New(client, cfg, notifier)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := w.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestWatcherUpdateTitle(t *testing.T) {
	pr := &github.PullRequest{
		Number: 1,
		Title:  "Updated Title",
	}
	pr.Head.SHA = "sha123"

	client := &mockGitHubClient{
		prs: map[string]*github.PullRequest{
			"owner/repo/1": pr,
		},
		statuses: map[string]*github.CombinedStatus{
			"sha123": {State: "success", SHA: "sha123"},
		},
	}

	cfg := &config.Config{
		WatchedPRs: []config.WatchedPR{
			{
				Owner:          "owner",
				Repo:           "repo",
				Number:         1,
				Title:          "", // Empty title
				LastKnownState: "success",
			},
		},
	}

	notifier := &mockNotifier{}
	w := New(client, cfg, notifier)

	if err := w.checkPR(&cfg.WatchedPRs[0]); err != nil {
		t.Fatalf("checkPR failed: %v", err)
	}

	// Title should be updated
	if cfg.WatchedPRs[0].Title != "Updated Title" {
		t.Errorf("expected title to be updated, got %q", cfg.WatchedPRs[0].Title)
	}
}

func TestWatcherStatusError(t *testing.T) {
	pr := &github.PullRequest{
		Number: 1,
		Title:  "Test PR",
	}
	pr.Head.SHA = "sha123"

	client := &mockGitHubClient{
		prs: map[string]*github.PullRequest{
			"owner/repo/1": pr,
		},
		statuses: map[string]*github.CombinedStatus{},
		err:      fmt.Errorf("status fetch failed"),
	}

	cfg := &config.Config{
		WatchedPRs: []config.WatchedPR{
			{
				Owner:          "owner",
				Repo:           "repo",
				Number:         1,
				LastKnownState: "success",
			},
		},
	}

	notifier := &mockNotifier{}
	w := New(client, cfg, notifier)

	err := w.checkPR(&cfg.WatchedPRs[0])
	if err == nil {
		t.Error("expected error when status fetch fails")
	}
}

func TestWatcherCheckAllPRs(t *testing.T) {
	pr1 := &github.PullRequest{Number: 1, Title: "PR1"}
	pr1.Head.SHA = "sha1"
	pr2 := &github.PullRequest{Number: 2, Title: "PR2"}
	pr2.Head.SHA = "sha2"

	client := &mockGitHubClient{
		prs: map[string]*github.PullRequest{
			"owner/repo/1": pr1,
			"owner/repo/2": pr2,
		},
		statuses: map[string]*github.CombinedStatus{
			"sha1": {State: "success", SHA: "sha1"},
			"sha2": {State: "failure", SHA: "sha2"},
		},
	}

	cfg := &config.Config{
		PollIntervalSeconds: 1,
		WatchedPRs: []config.WatchedPR{
			{Owner: "owner", Repo: "repo", Number: 1, LastKnownState: "pending"},
			{Owner: "owner", Repo: "repo", Number: 2, LastKnownState: "pending"},
		},
	}

	notifier := &mockNotifier{}
	w := New(client, cfg, notifier)

	w.checkAllPRs()

	// Both PRs should be notified
	if len(notifier.events) != 2 {
		t.Errorf("expected 2 notifications, got %d", len(notifier.events))
	}
}

func TestWatcherCheckAllPRsWithError(t *testing.T) {
	// Create a client that will fail for one PR
	pr1 := &github.PullRequest{Number: 1, Title: "PR1"}
	pr1.Head.SHA = "sha1"

	client := &mockGitHubClient{
		prs: map[string]*github.PullRequest{
			"owner/repo/1": pr1,
		},
		statuses: map[string]*github.CombinedStatus{
			"sha1": {State: "success", SHA: "sha1"},
		},
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "nonexistent", "deep", "path", "config.json")

	oldConfigPath := config.ConfigPath
	defer func() { config.ConfigPath = oldConfigPath }()
	config.ConfigPath = func() (string, error) {
		return configPath, nil
	}

	cfg := &config.Config{
		PollIntervalSeconds: 1,
		WatchedPRs: []config.WatchedPR{
			{Owner: "owner", Repo: "repo", Number: 1, LastKnownState: "pending"},
			{Owner: "badowner", Repo: "badrepo", Number: 999, LastKnownState: "pending"},
		},
	}

	notifier := &mockNotifier{}
	w := New(client, cfg, notifier)

	// This should handle errors gracefully and print warnings
	w.checkAllPRs()

	// Only one PR should be successfully checked
	if len(notifier.events) != 1 {
		t.Errorf("expected 1 successful notification, got %d", len(notifier.events))
	}
}

func TestWatcherNoPRs(t *testing.T) {
	client := &mockGitHubClient{}
	cfg := &config.Config{
		PollIntervalSeconds: 1,
		WatchedPRs:          []config.WatchedPR{},
	}
	notifier := &mockNotifier{}
	w := New(client, cfg, notifier)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := w.Run(ctx)
	// Run returns nil immediately when there are no PRs
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}
