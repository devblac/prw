package watcher

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
)

// fakeSearcher answers SearchPullRequests by source (looked up from the query).
type fakeSearcher struct {
	mockGitHubClient
	results  map[string]*github.SearchResult
	errs     map[string]error
	searches int
}

func (f *fakeSearcher) SearchPullRequests(ctx context.Context, query string) (*github.SearchResult, error) {
	f.searches++
	for source, q := range autoWatchQueries {
		if q == query {
			if err := f.errs[source]; err != nil {
				return nil, err
			}
			if res := f.results[source]; res != nil {
				return res, nil
			}
			return &github.SearchResult{}, nil
		}
	}
	return nil, fmt.Errorf("unexpected query %q", query)
}

func found(numbers ...int) *github.SearchResult {
	res := &github.SearchResult{Total: len(numbers)}
	for _, n := range numbers {
		res.PRs = append(res.PRs, github.SearchResultPR{Owner: "owner", Repo: "repo", Number: n, Title: fmt.Sprintf("PR %d", n)})
	}
	return res
}

func saveConfig(t *testing.T, cfg *config.Config) {
	t.Helper()
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

func watchList(t *testing.T) string {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var items []string
	for _, pr := range cfg.WatchedPRs {
		src := pr.Source
		if src == "" {
			src = "manual"
		}
		items = append(items, fmt.Sprintf("%d:%s", pr.Number, src))
	}
	sort.Strings(items)
	return strings.Join(items, " ")
}

func TestSyncAutoWatch_AddsMatchesTaggedWithSource(t *testing.T) {
	useTempConfig(t)
	saveConfig(t, &config.Config{
		AutoWatch:  []string{config.AutoWatchMine, config.AutoWatchReviewRequested},
		WatchedPRs: []config.WatchedPR{{Owner: "owner", Repo: "repo", Number: 1}}, // watched by hand
	})
	s := &fakeSearcher{results: map[string]*github.SearchResult{
		config.AutoWatchMine:            found(1, 2),
		config.AutoWatchReviewRequested: found(3),
	}}

	res, err := SyncAutoWatch(context.Background(), s, []string{config.AutoWatchMine, config.AutoWatchReviewRequested})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 2 || len(res.Removed) != 0 {
		t.Errorf("added=%+v removed=%+v", res.Added, res.Removed)
	}
	if got := watchList(t); got != "1:manual 2:mine 3:review-requested" {
		t.Errorf("watch list = %s (a PR watched by hand must stay manual)", got)
	}
	cfg, _ := config.Load()
	for _, pr := range cfg.WatchedPRs {
		if pr.Number == 2 && pr.Title != "PR 2" {
			t.Errorf("title should come from the search result, got %q", pr.Title)
		}
	}
}

func TestSyncAutoWatch_DropsWithdrawnReviewRequestsButKeepsMine(t *testing.T) {
	useTempConfig(t)
	saveConfig(t, &config.Config{
		AutoWatch: []string{config.AutoWatchMine, config.AutoWatchReviewRequested},
		WatchedPRs: []config.WatchedPR{
			{Owner: "owner", Repo: "repo", Number: 10, Source: config.AutoWatchMine},            // merged: no longer matches
			{Owner: "owner", Repo: "repo", Number: 20, Source: config.AutoWatchReviewRequested}, // review withdrawn
			{Owner: "owner", Repo: "repo", Number: 21, Source: config.AutoWatchReviewRequested}, // still requested
			{Owner: "owner", Repo: "repo", Number: 30},                                          // manual
		},
	})
	s := &fakeSearcher{results: map[string]*github.SearchResult{
		config.AutoWatchMine:            found(),
		config.AutoWatchReviewRequested: found(21),
	}}
	s.prs = map[string]*github.PullRequest{"owner/repo/20": openReviewPR(20)}

	res, err := SyncAutoWatch(context.Background(), s, []string{config.AutoWatchMine, config.AutoWatchReviewRequested})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0].Number != 20 {
		t.Errorf("removed = %+v", res.Removed)
	}
	if got := watchList(t); got != "10:mine 21:review-requested 30:manual" {
		t.Errorf("watch list = %s (mine PRs are left for prw run to notify and drop)", got)
	}
}

func TestSyncAutoWatch_IncompleteResultsNeverRemove(t *testing.T) {
	useTempConfig(t)
	saveConfig(t, &config.Config{
		AutoWatch:  []string{config.AutoWatchReviewRequested},
		WatchedPRs: []config.WatchedPR{{Owner: "owner", Repo: "repo", Number: 20, Source: config.AutoWatchReviewRequested}},
	})
	partial := found(21)
	partial.Incomplete, partial.Total = true, 2
	s := &fakeSearcher{results: map[string]*github.SearchResult{config.AutoWatchReviewRequested: partial}}

	res, err := SyncAutoWatch(context.Background(), s, []string{config.AutoWatchReviewRequested})
	if err != nil {
		t.Fatal(err)
	}
	if got := watchList(t); got != "20:review-requested 21:review-requested" {
		t.Errorf("watch list = %s", got)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "returned 1 of 2") {
		t.Errorf("warnings = %v", res.Warnings)
	}
}

func TestSyncAutoWatch_OneFailedSearchDoesNotBlockTheOther(t *testing.T) {
	useTempConfig(t)
	saveConfig(t, &config.Config{
		AutoWatch:  []string{config.AutoWatchMine, config.AutoWatchReviewRequested},
		WatchedPRs: []config.WatchedPR{{Owner: "owner", Repo: "repo", Number: 20, Source: config.AutoWatchReviewRequested}},
	})
	s := &fakeSearcher{
		results: map[string]*github.SearchResult{config.AutoWatchMine: found(1)},
		errs:    map[string]error{config.AutoWatchReviewRequested: errors.New("boom")},
	}

	res, err := SyncAutoWatch(context.Background(), s, []string{config.AutoWatchMine, config.AutoWatchReviewRequested})
	if err != nil {
		t.Fatalf("a partial failure is a warning, got %v", err)
	}
	if got := watchList(t); got != "1:mine 20:review-requested" {
		t.Errorf("watch list = %s (failed search must not remove its PRs)", got)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "boom") {
		t.Errorf("warnings = %v", res.Warnings)
	}
}

func TestSyncAutoWatch_AllSearchesFailing(t *testing.T) {
	useTempConfig(t)
	saveConfig(t, &config.Config{AutoWatch: []string{config.AutoWatchMine}})
	boom := errors.New("network down")
	s := &fakeSearcher{errs: map[string]error{config.AutoWatchMine: boom}}

	if _, err := SyncAutoWatch(context.Background(), s, []string{config.AutoWatchMine}); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the search error", err)
	}
}

func TestSyncAutoWatch_SkipsSourceDisabledMeanwhile(t *testing.T) {
	useTempConfig(t)
	saveConfig(t, &config.Config{}) // `prw unwatch --mine` ran after the caller read the config
	s := &fakeSearcher{results: map[string]*github.SearchResult{config.AutoWatchMine: found(1)}}

	res, err := SyncAutoWatch(context.Background(), s, []string{config.AutoWatchMine})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 0 || watchList(t) != "" {
		t.Errorf("a disabled source must not add PRs: %+v / %s", res.Added, watchList(t))
	}
}

func TestSyncAutoWatch_UnknownSource(t *testing.T) {
	useTempConfig(t)
	saveConfig(t, &config.Config{AutoWatch: []string{"bogus"}})
	res, err := SyncAutoWatch(context.Background(), &fakeSearcher{}, []string{"bogus"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "unknown auto-watch source") {
		t.Errorf("warnings = %v", res.Warnings)
	}
}

func TestRun_SyncsOnStartupEvenWithEmptyWatchList(t *testing.T) {
	useTempConfig(t)
	cfg := &config.Config{PollIntervalSeconds: 60, AutoWatch: []string{config.AutoWatchMine}}
	saveConfig(t, cfg)
	s := &fakeSearcher{results: map[string]*github.SearchResult{config.AutoWatchMine: found(7)}}
	s.prs = map[string]*github.PullRequest{"owner/repo/7": newPR(7, "sha7")}
	s.statuses = map[string]*github.CombinedStatus{"sha7": {State: "success", TotalCount: 1}}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	New(s, cfg, &mockNotifier{}).Run(ctx)

	if s.searches != 1 {
		t.Errorf("searches = %d, want 1 on startup", s.searches)
	}
	loaded, _ := config.Load()
	if len(loaded.WatchedPRs) != 1 || loaded.WatchedPRs[0].LastKnownState != "success" {
		t.Errorf("the auto-watched PR should be added and checked on startup: %+v", loaded.WatchedPRs)
	}
}

func TestRun_RefreshesSearchesOnlyEveryAutoWatchInterval(t *testing.T) {
	useTempConfig(t)
	cfg := &config.Config{PollIntervalSeconds: 1, AutoWatch: []string{config.AutoWatchMine}}
	saveConfig(t, cfg)
	s := &fakeSearcher{}
	w := New(s, cfg, &mockNotifier{})

	start := time.Now()
	var mu sync.Mutex
	clock := start
	w.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	go func() {
		// After ~1.5s (startup + one tick), jump the clock past the interval.
		time.Sleep(1500 * time.Millisecond)
		mu.Lock()
		clock = start.Add(AutoWatchInterval + time.Second)
		mu.Unlock()
	}()
	w.Run(ctx)

	if s.searches != 2 {
		t.Errorf("searches = %d, want 2 (startup + once after the interval elapsed)", s.searches)
	}
}

func TestRunOnce_SyncsAutoWatch(t *testing.T) {
	useTempConfig(t)
	cfg := &config.Config{AutoWatch: []string{config.AutoWatchReviewRequested}}
	saveConfig(t, cfg)
	s := &fakeSearcher{results: map[string]*github.SearchResult{config.AutoWatchReviewRequested: found(3)}}
	s.prs = map[string]*github.PullRequest{"owner/repo/3": newPR(3, "sha3")}

	if err := New(s, cfg, &mockNotifier{}).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := watchList(t); got != "3:review-requested" {
		t.Errorf("watch list = %s", got)
	}
}

func TestRun_PicksUpAutoWatchEnabledWhileRunning(t *testing.T) {
	useTempConfig(t)
	cfg := &config.Config{PollIntervalSeconds: 1, WatchedPRs: []config.WatchedPR{watched(1, "success")}}
	saveConfig(t, cfg)
	s := &fakeSearcher{results: map[string]*github.SearchResult{config.AutoWatchMine: found(2)}}
	s.prs = map[string]*github.PullRequest{"owner/repo/1": newPR(1, "sha1"), "owner/repo/2": newPR(2, "sha2")}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	go func() {
		time.Sleep(300 * time.Millisecond)
		// `prw watch --mine` in another terminal.
		config.Update(func(c *config.Config) error { c.EnableAutoWatch(config.AutoWatchMine); return nil })
	}()
	New(s, cfg, &mockNotifier{}).Run(ctx)

	if s.searches != 1 {
		t.Errorf("searches = %d, want 1 after auto-watch was enabled", s.searches)
	}
	if got := watchList(t); got != "1:manual 2:mine" {
		t.Errorf("watch list = %s", got)
	}
}

func TestRunOnce_NotifiesReviewRequestMergedWhileStopped(t *testing.T) {
	// The PR was merged while prw wasn't running: the is:open search no longer returns
	// it, which must not be mistaken for a withdrawn review request.
	useTempConfig(t)
	cfg := &config.Config{
		AutoWatch: []string{config.AutoWatchReviewRequested},
		WatchedPRs: []config.WatchedPR{
			{Owner: "owner", Repo: "repo", Number: 20, Source: config.AutoWatchReviewRequested, LastKnownState: "success"},
		},
	}
	saveConfig(t, cfg)
	merged := newPR(20, "sha20")
	merged.State, merged.Merged = "closed", true
	s := &fakeSearcher{results: map[string]*github.SearchResult{config.AutoWatchReviewRequested: found()}}
	s.prs = map[string]*github.PullRequest{"owner/repo/20": merged}
	notifier := &mockNotifier{}

	if err := New(s, cfg, notifier).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notifier.events) != 1 || notifier.events[0].CurrentState != "merged" {
		t.Errorf("events = %+v, want one merged notification", notifier.events)
	}
	if got := watchList(t); got != "" {
		t.Errorf("watch list = %q, want the merged PR dropped after notifying", got)
	}
}

func openReviewPR(number int) *github.PullRequest {
	pr := newPR(number, fmt.Sprintf("sha%d", number))
	pr.State = "open"
	return pr
}

// failingLookups answers GetPullRequest with errs (keyed like prs) for the given PRs.
type failingLookups struct {
	*fakeSearcher
	errs map[string]error
}

func (f *failingLookups) GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error) {
	if err := f.errs[fmt.Sprintf("%s/%s/%d", owner, repo, number)]; err != nil {
		return nil, err
	}
	return f.fakeSearcher.GetPullRequest(ctx, owner, repo, number)
}

func TestSyncAutoWatch_RemovesReviewRequestsOnlyWhenProvenWithdrawn(t *testing.T) {
	useTempConfig(t)
	reviewPR := func(n int) config.WatchedPR {
		return config.WatchedPR{Owner: "owner", Repo: "repo", Number: n, Source: config.AutoWatchReviewRequested}
	}
	saveConfig(t, &config.Config{
		AutoWatch:  []string{config.AutoWatchReviewRequested},
		WatchedPRs: []config.WatchedPR{reviewPR(1), reviewPR(2), reviewPR(3)},
	})
	s := &failingLookups{
		fakeSearcher: &fakeSearcher{results: map[string]*github.SearchResult{config.AutoWatchReviewRequested: found()}},
		errs: map[string]error{
			"owner/repo/2": &github.APIError{StatusCode: 404}, // deleted or no access
			"owner/repo/3": errors.New("connection reset"),
		},
	}
	s.prs = map[string]*github.PullRequest{"owner/repo/1": openReviewPR(1)}

	if _, err := SyncAutoWatch(context.Background(), s, []string{config.AutoWatchReviewRequested}); err != nil {
		t.Fatal(err)
	}
	if got := watchList(t); got != "3:review-requested" {
		t.Errorf("watch list = %q, want #1 (open) and #2 (404) dropped, #3 (lookup failed) kept", got)
	}
}

func TestSyncAutoWatch_InterruptedDuringLookupsChangesNothing(t *testing.T) {
	useTempConfig(t)
	saveConfig(t, &config.Config{
		AutoWatch: []string{config.AutoWatchReviewRequested},
		WatchedPRs: []config.WatchedPR{
			{Owner: "owner", Repo: "repo", Number: 1, Source: config.AutoWatchReviewRequested},
			{Owner: "owner", Repo: "repo", Number: 2, Source: config.AutoWatchReviewRequested},
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &failingLookups{
		fakeSearcher: &fakeSearcher{results: map[string]*github.SearchResult{config.AutoWatchReviewRequested: found(9)}},
	}
	s.prs = map[string]*github.PullRequest{"owner/repo/1": openReviewPR(1)}
	s.errs = map[string]error{"owner/repo/2": context.Canceled}
	cancelOnLookup := &cancellingSearcher{failingLookups: s, cancel: cancel}

	if _, err := SyncAutoWatch(ctx, cancelOnLookup, []string{config.AutoWatchReviewRequested}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := watchList(t); got != "1:review-requested 2:review-requested" {
		t.Errorf("watch list = %q, want it untouched (no removals, no additions)", got)
	}
}

// cancellingSearcher cancels the context (Ctrl-C) when the first PR lookup starts.
type cancellingSearcher struct {
	*failingLookups
	cancel context.CancelFunc
}

func (c *cancellingSearcher) GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error) {
	c.cancel()
	return c.failingLookups.GetPullRequest(ctx, owner, repo, number)
}
