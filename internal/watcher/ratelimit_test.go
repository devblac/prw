package watcher

import (
	"context"
	"testing"
	"time"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
)

// countingClient counts GetPullRequest calls and can fail them with a fixed error.
type countingClient struct {
	mockGitHubClient
	calls int
	prErr error
}

func (c *countingClient) GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error) {
	c.calls++
	if c.prErr != nil {
		return nil, c.prErr
	}
	return c.mockGitHubClient.GetPullRequest(context.Background(), owner, repo, number)
}

func TestCheckAllPRs_StopsAndPausesOnRateLimit(t *testing.T) {
	useTempConfig(t)
	reset := time.Date(2026, 1, 1, 12, 30, 0, 0, time.UTC)
	client := &countingClient{prErr: &github.APIError{StatusCode: 403, RateLimited: true, ResetAt: reset}}

	prs := []config.WatchedPR{watched(1, "success"), watched(2, "failure"), watched(3, "pending")}
	cfg := &config.Config{WatchedPRs: append([]config.WatchedPR(nil), prs...)}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	w := New(client, cfg, &mockNotifier{})
	w.now = func() time.Time { return reset.Add(-10 * time.Minute) }

	w.checkAllPRs(context.Background())

	if client.calls != 1 {
		t.Errorf("must stop after the first rate-limited request, made %d", client.calls)
	}
	if !w.pausedUntil.Equal(reset) {
		t.Errorf("pausedUntil = %v, want %v", w.pausedUntil, reset)
	}
	if len(w.config.WatchedPRs) != 3 {
		t.Fatalf("rate limit must not drop PRs from the watch list: %+v", w.config.WatchedPRs)
	}
	for i, pr := range w.config.WatchedPRs {
		if pr.Number != prs[i].Number || pr.LastKnownState != prs[i].LastKnownState {
			t.Errorf("PR %d changed: %+v", i, pr)
		}
	}
}

func TestPauseForRateLimit_DefaultsWhenResetUnknown(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	w := New(&mockGitHubClient{}, &config.Config{}, &mockNotifier{})
	w.now = func() time.Time { return now }

	w.pauseForRateLimit(time.Time{})
	if !w.pausedUntil.Equal(now.Add(defaultRateLimitPause)) {
		t.Errorf("pausedUntil = %v, want now+%s", w.pausedUntil, defaultRateLimitPause)
	}
}

func TestRun_SkipsTicksWhilePaused(t *testing.T) {
	useTempConfig(t)
	cfg := &config.Config{PollIntervalSeconds: 1, WatchedPRs: []config.WatchedPR{watched(1, "success")}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(time.Hour)
	client := &countingClient{prErr: &github.APIError{StatusCode: 429, RateLimited: true, ResetAt: reset}}
	w := New(client, cfg, &mockNotifier{})

	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	// The startup check hits the limit; the ~2 ticks that follow must be skipped.
	if client.calls != 1 {
		t.Errorf("expected only the startup request while paused, got %d", client.calls)
	}
}

func TestCheckAllPRs_OtherErrorsDoNotPause(t *testing.T) {
	useTempConfig(t)
	client := &countingClient{prErr: &github.APIError{StatusCode: 404}}
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, ""), watched(2, "")}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	w := New(client, cfg, &mockNotifier{})

	w.checkAllPRs(context.Background())

	if client.calls != 2 {
		t.Errorf("a non-rate-limit error must not stop the round, made %d calls", client.calls)
	}
	if !w.pausedUntil.IsZero() {
		t.Errorf("must not pause on a 404, pausedUntil = %v", w.pausedUntil)
	}
}
