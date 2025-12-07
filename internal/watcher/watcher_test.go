package watcher

import (
	"context"
	"fmt"
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
}

func (m *mockNotifier) Notify(event *notify.StatusChangeEvent) error {
	m.events = append(m.events, event)
	return nil
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
