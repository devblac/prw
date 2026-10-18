package watcher

import (
	"context"
	"testing"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
)

func closedPR(number int, merged bool) *github.PullRequest {
	pr := &github.PullRequest{Number: number, Title: "PR", State: "closed", Merged: merged}
	pr.Head.SHA = "sha-closed"
	return pr
}

func TestCheckPR_MergedPRIsRemoved(t *testing.T) {
	client := &mockGitHubClient{prs: map[string]*github.PullRequest{"owner/repo/1": closedPR(1, true)}}
	notifier := &mockNotifier{}
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, "success")}}
	w := New(client, cfg, notifier)

	closed, err := w.checkPR(context.Background(), &cfg.WatchedPRs[0])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !closed {
		t.Fatal("expected checkPR to report the PR as closed")
	}
	if len(notifier.events) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifier.events))
	}
	event := notifier.events[0]
	if event.CurrentState != "merged" {
		t.Errorf("CurrentState = %q, want merged", event.CurrentState)
	}
	if event.PreviousState != "success" {
		t.Errorf("PreviousState = %q, want success", event.PreviousState)
	}
}

func TestCheckPR_ClosedWithoutMergeIsRemoved(t *testing.T) {
	client := &mockGitHubClient{prs: map[string]*github.PullRequest{"owner/repo/1": closedPR(1, false)}}
	notifier := &mockNotifier{}
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, "failure")}}
	w := New(client, cfg, notifier)

	closed, err := w.checkPR(context.Background(), &cfg.WatchedPRs[0])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !closed {
		t.Fatal("expected checkPR to report the PR as closed")
	}
	if got := notifier.events[0].CurrentState; got != "closed" {
		t.Errorf("CurrentState = %q, want closed", got)
	}
}

func TestCheckPR_MergedNotifiesEvenWithFailFilter(t *testing.T) {
	client := &mockGitHubClient{prs: map[string]*github.PullRequest{"owner/repo/1": closedPR(1, true)}}
	notifier := &mockNotifier{}
	cfg := &config.Config{NotificationFilter: config.NotificationFilterFail, WatchedPRs: []config.WatchedPR{watched(1, "success")}}
	w := New(client, cfg, notifier)

	if _, err := w.checkPR(context.Background(), &cfg.WatchedPRs[0]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notifier.events) != 1 {
		t.Fatalf("a merge must notify regardless of notification_filter, got %d events", len(notifier.events))
	}
}

func TestCheckPR_OpenPRIsNotRemoved(t *testing.T) {
	pr := newPR(1, "sha1")
	pr.State = "open"
	client := &mockGitHubClient{
		prs:      map[string]*github.PullRequest{"owner/repo/1": pr},
		statuses: map[string]*github.CombinedStatus{"sha1": {State: "success", SHA: "sha1"}},
	}
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, "pending")}}
	w := New(client, cfg, &mockNotifier{})

	closed, err := w.checkPR(context.Background(), &cfg.WatchedPRs[0])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if closed {
		t.Error("an open PR must not be reported as closed")
	}
}

func TestCheckAllPRs_RemovesOnlyClosedPRs(t *testing.T) {
	useTempConfig(t)

	onDisk := &config.Config{PollIntervalSeconds: 20, WatchedPRs: []config.WatchedPR{watched(1, "pending"), watched(2, "success")}}
	if err := onDisk.Save(); err != nil {
		t.Fatal(err)
	}

	client := &mockGitHubClient{
		prs: map[string]*github.PullRequest{
			"owner/repo/1": newPR(1, "sha1"), // stays open
			"owner/repo/2": closedPR(2, true),
		},
		statuses: map[string]*github.CombinedStatus{"sha1": {State: "success", SHA: "sha1"}},
	}
	inMemory := &config.Config{PollIntervalSeconds: 20, WatchedPRs: []config.WatchedPR{watched(1, "pending"), watched(2, "success")}}
	w := New(client, inMemory, &mockNotifier{})

	w.checkAllPRs(context.Background())

	if len(w.config.WatchedPRs) != 1 || w.config.WatchedPRs[0].Number != 1 {
		t.Fatalf("in-memory watch list after checkAllPRs = %+v, want only #1", w.config.WatchedPRs)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.WatchedPRs) != 1 || loaded.WatchedPRs[0].Number != 1 {
		t.Fatalf("persisted watch list = %+v, want only #1", loaded.WatchedPRs)
	}
	if loaded.WatchedPRs[0].LastKnownState != "success" {
		t.Errorf("open PR's state was not updated: %+v", loaded.WatchedPRs[0])
	}
}

func TestCheckAllPRs_RemovalSurvivesConcurrentChange(t *testing.T) {
	useTempConfig(t)

	onDisk := &config.Config{PollIntervalSeconds: 20, WatchedPRs: []config.WatchedPR{watched(1, "success")}}
	if err := onDisk.Save(); err != nil {
		t.Fatal(err)
	}
	inMemory := &config.Config{PollIntervalSeconds: 20, WatchedPRs: []config.WatchedPR{watched(1, "success")}}
	client := &mockGitHubClient{prs: map[string]*github.PullRequest{"owner/repo/1": closedPR(1, true)}}
	w := New(client, inMemory, &mockNotifier{})

	// Someone runs `prw watch` on a second PR while this poll is in flight.
	if err := config.Update(func(c *config.Config) error {
		c.AddPR(watched(2, ""))
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	w.checkAllPRs(context.Background())

	loaded, _ := config.Load()
	if len(loaded.WatchedPRs) != 1 || loaded.WatchedPRs[0].Number != 2 {
		t.Fatalf("watch list = %+v, want only the PR added concurrently (#1 removed, #2 kept)", loaded.WatchedPRs)
	}
}
