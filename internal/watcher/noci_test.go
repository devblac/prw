package watcher

import (
	"context"
	"testing"
	"time"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
)

func quietPR(number int, sha string, quietFor time.Duration, now time.Time) *github.PullRequest {
	pr := newPR(number, sha)
	pr.State = "open"
	pr.UpdatedAt = now.Add(-quietFor)
	return pr
}

func TestCheckPR_ReportsNoCIWithoutNotifying(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	client := &mockGitHubClient{prs: map[string]*github.PullRequest{"owner/repo/1": quietPR(1, "sha1", time.Hour, now)}}
	notifier := &mockNotifier{}
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, "pending")}}
	w := New(client, cfg, notifier)
	w.now = func() time.Time { return now }

	if _, err := w.checkPR(context.Background(), &cfg.WatchedPRs[0]); err != nil {
		t.Fatal(err)
	}
	if got := cfg.WatchedPRs[0].LastKnownState; got != github.StateNoCI {
		t.Errorf("state = %q, want no_ci", got)
	}
	if len(notifier.events) != 0 {
		t.Errorf("pending → no_ci must not notify, got %+v", notifier.events[0])
	}
}

func TestCheckPR_StaysPendingDuringGracePeriod(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	client := &mockGitHubClient{prs: map[string]*github.PullRequest{"owner/repo/1": quietPR(1, "sha1", 20*time.Second, now)}}
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, "")}}
	w := New(client, cfg, &mockNotifier{})
	w.now = func() time.Time { return now }

	w.checkPR(context.Background(), &cfg.WatchedPRs[0])
	if got := cfg.WatchedPRs[0].LastKnownState; got != "pending" {
		t.Errorf("a PR pushed 20s ago must stay pending while CI registers, got %q", got)
	}
}

func TestCheckPR_NotifiesWhenCIShowsUpLater(t *testing.T) {
	// e.g. a stacked PR is retargeted to main and CI starts running.
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	client := &mockGitHubClient{
		prs:       map[string]*github.PullRequest{"owner/repo/1": quietPR(1, "sha1", time.Minute, now)},
		checkRuns: map[string][]github.CheckRun{"sha1": {{Name: "Test", Status: "completed", Conclusion: "success"}}},
	}
	notifier := &mockNotifier{}
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, github.StateNoCI)}}
	w := New(client, cfg, notifier)
	w.now = func() time.Time { return now }

	w.checkPR(context.Background(), &cfg.WatchedPRs[0])
	if len(notifier.events) != 1 || notifier.events[0].PreviousState != github.StateNoCI || notifier.events[0].CurrentState != "success" {
		t.Errorf("events = %+v, want one no_ci → success", notifier.events)
	}
}

func TestCheckPR_StaysNoCIWhenOnlyMetadataChanges(t *testing.T) {
	// A comment or label bumps updated_at, but no check or status appeared.
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	client := &mockGitHubClient{prs: map[string]*github.PullRequest{"owner/repo/1": quietPR(1, "sha1", 10*time.Second, now)}}
	notifier := &mockNotifier{}
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, github.StateNoCI)}}
	w := New(client, cfg, notifier)
	w.now = func() time.Time { return now }

	if _, err := w.checkPR(context.Background(), &cfg.WatchedPRs[0]); err != nil {
		t.Fatal(err)
	}
	if got := cfg.WatchedPRs[0].LastKnownState; got != github.StateNoCI {
		t.Errorf("state = %q, want no_ci", got)
	}
	if len(notifier.events) != 0 {
		t.Errorf("a comment must not notify no_ci → pending, got %+v", notifier.events[0])
	}
}

func TestCheckPR_NoCIToPendingWhenCIStartsAfterAPush(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	client := &mockGitHubClient{
		prs:       map[string]*github.PullRequest{"owner/repo/1": quietPR(1, "sha2", 10*time.Second, now)},
		checkRuns: map[string][]github.CheckRun{"sha2": {{Name: "Test", Status: "queued"}}},
	}
	notifier := &mockNotifier{}
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, github.StateNoCI)}}
	w := New(client, cfg, notifier)
	w.now = func() time.Time { return now }

	w.checkPR(context.Background(), &cfg.WatchedPRs[0])
	if got := cfg.WatchedPRs[0].LastKnownState; got != "pending" || len(notifier.events) != 1 {
		t.Errorf("state = %q events = %d, want pending with one notification once a check exists", got, len(notifier.events))
	}
}
