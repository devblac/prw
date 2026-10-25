package watcher

import (
	"context"
	"testing"
	"time"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
)

func bitbucketPR(number int, state string) config.WatchedPR {
	pr := watched(number, state)
	pr.Owner, pr.Repo, pr.Host = "acme", "api", forge.Bitbucket
	return pr
}

func TestCheckAllPRs_UsesEachHostsClient(t *testing.T) {
	useTempConfig(t)
	gh := &countingClient{mockGitHubClient: mockGitHubClient{prs: map[string]*github.PullRequest{"owner/repo/1": newPR(1, "sha1")}}}
	bb := &countingClient{mockGitHubClient: mockGitHubClient{prs: map[string]*github.PullRequest{"acme/api/2": newPR(2, "sha2")}}}
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, "pending"), bitbucketPR(2, "pending")}}
	cfg.Save()
	w := New(gh, cfg, &mockNotifier{})
	w.SetClient(forge.Bitbucket, bb)

	w.checkAllPRs(context.Background())
	if gh.calls != 1 || bb.calls != 1 {
		t.Errorf("github calls=%d bitbucket calls=%d, want one each", gh.calls, bb.calls)
	}
}

func TestCheckAllPRs_RateLimitPausesOnlyThatHost(t *testing.T) {
	useTempConfig(t)
	gh := &countingClient{mockGitHubClient: mockGitHubClient{prs: map[string]*github.PullRequest{
		"owner/repo/1": newPR(1, "sha1"), "owner/repo/3": newPR(3, "sha3")}}}
	bb := &countingClient{prErr: &github.APIError{StatusCode: 429, RateLimited: true}}
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{bitbucketPR(2, "success"), watched(1, ""), bitbucketPR(4, "success"), watched(3, "")}}
	cfg.Save()
	w := New(gh, cfg, &mockNotifier{})
	w.SetClient(forge.Bitbucket, bb)

	w.checkAllPRs(context.Background())
	if bb.calls != 1 {
		t.Errorf("Bitbucket must stop after its first rate-limited request, made %d", bb.calls)
	}
	if gh.calls != 2 {
		t.Errorf("GitHub PRs must still be checked while Bitbucket is limited, made %d", gh.calls)
	}
	if w.pausedUntil[forge.Bitbucket].IsZero() || !w.pausedUntil[forge.GitHub].IsZero() {
		t.Errorf("pausedUntil = %v", w.pausedUntil)
	}
	if len(w.config.WatchedPRs) != 4 {
		t.Errorf("no PR may be dropped: %+v", w.config.WatchedPRs)
	}
}

func TestCheckAllPRs_BitbucketMinInterval(t *testing.T) {
	useTempConfig(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	bb := &countingClient{mockGitHubClient: mockGitHubClient{prs: map[string]*github.PullRequest{"acme/api/2": newPR(2, "sha2")}}}
	recent, stale := bitbucketPR(2, "success"), bitbucketPR(2, "success")
	recent.LastChecked = now.Add(-20 * time.Second)
	stale.LastChecked = now.Add(-BitbucketMinInterval)

	for _, tt := range []struct {
		pr   config.WatchedPR
		want int
	}{{recent, 0}, {stale, 1}, {bitbucketPR(2, ""), 1}} {
		bb.calls = 0
		cfg := &config.Config{WatchedPRs: []config.WatchedPR{tt.pr}}
		cfg.Save()
		w := New(nil, cfg, &mockNotifier{})
		w.SetClient(forge.Bitbucket, bb)
		w.now = func() time.Time { return now }
		w.checkAllPRs(context.Background())
		if bb.calls != tt.want {
			t.Errorf("last checked %v ago: %d calls, want %d", now.Sub(tt.pr.LastChecked), bb.calls, tt.want)
		}
	}
}

func TestCheckAllPRs_NoClientForAHostKeepsItsPRs(t *testing.T) {
	useTempConfig(t)
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, "success")}}
	cfg.Save()
	var nilClient *github.Client // no GitHub token: run passes a nil client
	w := New(nilClient, cfg, &mockNotifier{})

	w.checkAllPRs(context.Background())
	w.checkAllPRs(context.Background())
	if len(w.config.WatchedPRs) != 1 || w.config.WatchedPRs[0].LastKnownState != "success" {
		t.Errorf("watch list = %+v", w.config.WatchedPRs)
	}
}
