package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
)

// setupAutoWatchCmd points commands at a temp config (with a token) and a fake GitHub
// whose search returns mine/reviewRequested PR numbers in owner/repo.
func setupAutoWatchCmd(t *testing.T, mine, reviewRequested []int) *int {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".prw", "config.json")
	oldPath := config.ConfigPath
	config.ConfigPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { config.ConfigPath = oldPath })
	cfg := config.DefaultConfig()
	cfg.GitHubToken = "test-token"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	searches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search/issues":
			searches++
			numbers := mine
			if strings.Contains(r.URL.Query().Get("q"), "review-requested:@me") {
				numbers = reviewRequested
			}
			items := make([]string, len(numbers))
			for i, n := range numbers {
				items[i] = fmt.Sprintf(`{"number":%d,"title":"PR %d","html_url":"https://github.com/owner/repo/pull/%d","pull_request":{}}`, n, n, n)
			}
			fmt.Fprintf(w, `{"total_count":%d,"items":[%s]}`, len(items), strings.Join(items, ","))
		case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/pulls/"):
			n := strings.TrimPrefix(r.URL.Path, "/repos/owner/repo/pulls/")
			fmt.Fprintf(w, `{"number":%s,"title":"PR %s","state":"open","head":{"sha":"sha%s"}}`, n, n, n)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	oldClient := newGitHubClient
	newGitHubClient = func(token string) *github.Client {
		c := github.NewClient(token)
		c.BaseURL = server.URL
		c.HTTPClient = server.Client()
		return c
	}
	t.Cleanup(func() { newGitHubClient = oldClient })

	resetFlags := func() {
		watchMine, watchReviewRequested, unwatchMine, unwatchReviewRequested = false, false, false, false
	}
	resetFlags()
	t.Cleanup(resetFlags)
	return &searches
}

func watchListSummary(t *testing.T) string {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, pr := range cfg.WatchedPRs {
		src := pr.Source
		if src == "" {
			src = "manual"
		}
		parts = append(parts, fmt.Sprintf("%d:%s", pr.Number, src))
	}
	return strings.Join(parts, " ")
}

func TestWatchCmd_MineAddsSubscriptionAndCurrentPRs(t *testing.T) {
	searches := setupAutoWatchCmd(t, []int{1, 2}, nil)
	watchMine = true

	out, err := captureStdout(func() error { return watchCmd.RunE(watchCmd, nil) })
	if err != nil {
		t.Fatalf("watch --mine: %v", err)
	}
	for _, want := range []string{"Now auto-watching your open PRs.", "+ owner/repo#1 - PR 1", "+ owner/repo#2 - PR 2", "searches again every 5 minutes"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	cfg, _ := config.Load()
	if !cfg.HasAutoWatch(config.AutoWatchMine) || cfg.HasAutoWatch(config.AutoWatchReviewRequested) {
		t.Errorf("AutoWatch = %v", cfg.AutoWatch)
	}
	if got := watchListSummary(t); got != "1:mine 2:mine" {
		t.Errorf("watch list = %s", got)
	}

	// Running it again is idempotent.
	out, err = captureStdout(func() error { return watchCmd.RunE(watchCmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Already auto-watching your open PRs.") || !strings.Contains(out, "No new PRs to add right now.") {
		t.Errorf("second run output:\n%s", out)
	}
	if *searches != 2 {
		t.Errorf("searches = %d", *searches)
	}
}

func TestWatchCmd_BothSourcesAndAURL(t *testing.T) {
	setupAutoWatchCmd(t, []int{1}, []int{2})
	watchMine, watchReviewRequested = true, true

	if _, err := captureStdout(func() error {
		return watchCmd.RunE(watchCmd, []string{"https://github.com/owner/repo/pull/9"})
	}); err != nil {
		t.Fatal(err)
	}
	if got := watchListSummary(t); got != "9:manual 1:mine 2:review-requested" {
		t.Errorf("watch list = %s", got)
	}
}

func TestWatchCmd_ByHandClaimsAnAutoWatchedPR(t *testing.T) {
	setupAutoWatchCmd(t, []int{1}, nil)
	watchMine = true
	captureStdout(func() error { return watchCmd.RunE(watchCmd, nil) })
	watchMine = false

	out, err := captureStdout(func() error { return watchCmd.RunE(watchCmd, []string{"https://github.com/owner/repo/pull/1"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already being watched") {
		t.Errorf("output: %s", out)
	}
	if got := watchListSummary(t); got != "1:manual" {
		t.Errorf("watching by hand must claim the PR, got %s", got)
	}

	// ... so turning auto-watch off keeps it.
	unwatchMine = true
	if _, err := captureStdout(func() error { return unwatchCmd.RunE(unwatchCmd, nil) }); err != nil {
		t.Fatal(err)
	}
	if got := watchListSummary(t); got != "1:manual" {
		t.Errorf("watch list after unwatch --mine = %s", got)
	}
}

func TestUnwatchCmd_MineRemovesSubscriptionAndItsPRs(t *testing.T) {
	setupAutoWatchCmd(t, []int{1, 2}, []int{3})
	watchMine, watchReviewRequested = true, true
	captureStdout(func() error { return watchCmd.RunE(watchCmd, []string{"https://github.com/owner/repo/pull/9"}) })

	unwatchMine = true
	out, err := captureStdout(func() error { return unwatchCmd.RunE(unwatchCmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Stopped auto-watching your open PRs; removed 2 PR(s) it had added.") {
		t.Errorf("output: %s", out)
	}
	cfg, _ := config.Load()
	if cfg.HasAutoWatch(config.AutoWatchMine) || !cfg.HasAutoWatch(config.AutoWatchReviewRequested) {
		t.Errorf("AutoWatch = %v", cfg.AutoWatch)
	}
	if got := watchListSummary(t); got != "9:manual 3:review-requested" {
		t.Errorf("watch list = %s", got)
	}

	out, _ = captureStdout(func() error { return unwatchCmd.RunE(unwatchCmd, nil) })
	if !strings.Contains(out, "Was not auto-watching your open PRs.") {
		t.Errorf("second unwatch --mine output: %s", out)
	}
}

func TestWatchCmd_Args(t *testing.T) {
	setupAutoWatchCmd(t, nil, nil)

	if err := watchCmd.Args(watchCmd, nil); err == nil || !strings.Contains(err.Error(), "--mine") {
		t.Errorf("no URL and no flag must fail with a hint, got %v", err)
	}
	if err := watchCmd.Args(watchCmd, []string{"a", "b"}); err == nil {
		t.Error("two URLs must fail")
	}
	if err := watchCmd.Args(watchCmd, []string{"https://github.com/o/r/pull/1"}); err != nil {
		t.Errorf("one URL: %v", err)
	}
	watchMine = true
	if err := watchCmd.Args(watchCmd, nil); err != nil {
		t.Errorf("--mine without URL: %v", err)
	}
	if err := watchCmd.Args(watchCmd, []string{"a", "b"}); err == nil {
		t.Error("--mine with two URLs must fail")
	}

	if err := unwatchCmd.Args(unwatchCmd, nil); err == nil {
		t.Error("unwatch with nothing must fail")
	}
	unwatchReviewRequested = true
	if err := unwatchCmd.Args(unwatchCmd, nil); err != nil {
		t.Errorf("unwatch --review-requested: %v", err)
	}
}

func TestWatchCmd_MineNeedsToken(t *testing.T) {
	setupAutoWatchCmd(t, nil, nil)
	config.Update(func(c *config.Config) error { c.GitHubToken = ""; return nil })
	t.Setenv("GITHUB_TOKEN", "")
	watchMine = true

	if err := watchCmd.RunE(watchCmd, nil); err != errMissingToken {
		t.Errorf("err = %v, want errMissingToken", err)
	}
	if cfg, _ := config.Load(); len(cfg.AutoWatch) != 0 {
		t.Error("auto-watch must not be enabled without a token")
	}
}

func TestListAndConfigShow_ShowAutoWatch(t *testing.T) {
	setupAutoWatchCmd(t, []int{1}, nil)
	watchMine = true
	captureStdout(func() error { return watchCmd.RunE(watchCmd, []string{"https://github.com/owner/repo/pull/9"}) })

	out, err := captureStdout(func() error { return listCmd.RunE(listCmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Auto-watching your open PRs (--mine).") || !strings.Contains(out, "ADDED BY") {
		t.Errorf("list output:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	var row1, row9 string
	for _, l := range lines {
		if strings.Contains(l, "#1 ") {
			row1 = l
		}
		if strings.Contains(l, "#9 ") {
			row9 = l
		}
	}
	if !strings.Contains(row1, "--mine") || !strings.Contains(row9, "you") {
		t.Errorf("ADDED BY column wrong:\n%s", out)
	}

	oldJSON := listJSON
	listJSON = true
	defer func() { listJSON = oldJSON }()
	out, _ = captureStdout(func() error { return listCmd.RunE(listCmd, nil) })
	if !strings.Contains(out, `"source": "mine"`) {
		t.Errorf("JSON list should include the source:\n%s", out)
	}

	out, _ = captureStdout(func() error { return configShowCmd.RunE(configShowCmd, nil) })
	if !strings.Contains(out, "auto_watch: mine") {
		t.Errorf("config show output:\n%s", out)
	}
}
