package watcher

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/devblac/prw/internal/bitbucket"
	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
)

const me = "{me}"

// fakeLister serves Bitbucket's list endpoints from maps keyed by workspace and
// "workspace/repo".
type fakeLister struct {
	mockGitHubClient
	workspaces []string
	mine       map[string][]bitbucket.ListedPR
	repos      map[string][]string
	reviews    map[string][]bitbucket.ListedPR
	errs       map[string]error // by workspace or "workspace/repo"
	userCalls  int
	scans      int
}

func (f *fakeLister) CurrentUser(ctx context.Context) (string, error) {
	f.userCalls++
	return me, nil
}

func (f *fakeLister) Workspaces(ctx context.Context) ([]string, error) { return f.workspaces, nil }

func (f *fakeLister) UserPullRequests(ctx context.Context, ws, user, state string, since time.Time) ([]bitbucket.ListedPR, error) {
	if user != me || state != "OPEN" {
		return nil, fmt.Errorf("unexpected user %s / state %s", user, state)
	}
	return f.mine[ws], f.errs[ws]
}

func (f *fakeLister) RecentRepos(ctx context.Context, ws string, since time.Time, max int) ([]string, error) {
	f.scans++
	return f.repos[ws], f.errs[ws]
}

func (f *fakeLister) ReviewPullRequests(ctx context.Context, ws, repo, user string) ([]bitbucket.ListedPR, error) {
	return f.reviews[ws+"/"+repo], f.errs[ws+"/"+repo]
}

func listed(ws, repo string, number int, reviewedBy ...string) bitbucket.ListedPR {
	return bitbucket.ListedPR{Workspace: ws, Repo: repo, Number: number, Title: fmt.Sprintf("PR %d", number), State: "OPEN", ReviewedBy: reviewedBy}
}

func numbers(f *Found) []int {
	var n []int
	for _, pr := range f.PRs {
		n = append(n, pr.Number)
	}
	return n
}

func TestBitbucketFinder_MineAcrossWorkspaces(t *testing.T) {
	api := &fakeLister{
		workspaces: []string{"acme", "labs"},
		mine:       map[string][]bitbucket.ListedPR{"acme": {listed("acme", "api", 1)}, "labs": {listed("labs", "web", 2)}},
	}
	f := &BitbucketFinder{API: api}
	got, err := f.Find(context.Background(), config.AutoWatchMine)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(numbers(got)) != "[1 2]" || got.Partial != "" {
		t.Errorf("found %v, partial %q", numbers(got), got.Partial)
	}
	if got.PRs[1].Owner != "labs" || got.PRs[1].Repo != "web" {
		t.Errorf("PR = %+v", got.PRs[1])
	}
	f.Find(context.Background(), config.AutoWatchMine)
	if api.userCalls != 1 {
		t.Errorf("the user must be looked up once, was %d times", api.userCalls)
	}
}

func TestBitbucketFinder_FailedWorkspaceIsPartial(t *testing.T) {
	api := &fakeLister{
		mine: map[string][]bitbucket.ListedPR{"acme": {listed("acme", "api", 1)}},
		errs: map[string]error{"labs": errors.New("boom")},
	}
	f := &BitbucketFinder{API: api, User: me, Workspaces: []string{"acme", "labs"}}
	got, err := f.Find(context.Background(), config.AutoWatchMine)
	if err != nil || got.Partial == "" || len(got.PRs) != 1 {
		t.Errorf("got %+v, %v; want acme's PR and a partial note", got, err)
	}

	f.Workspaces = []string{"labs"}
	if _, err := f.Find(context.Background(), config.AutoWatchMine); err == nil {
		t.Error("every workspace failing must be an error")
	}
}

func TestBitbucketFinder_ReviewsSkipReviewedAndRescanEvery15Minutes(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	api := &fakeLister{
		repos: map[string][]string{"acme": {"api", "web"}},
		reviews: map[string][]bitbucket.ListedPR{
			"acme/api": {listed("acme", "api", 1), listed("acme", "api", 2, "{other}", "")},
			"acme/web": {listed("acme", "web", 3, me)}, // already approved by me
		},
	}
	f := &BitbucketFinder{API: api, User: me, Workspaces: []string{"acme"}, Now: func() time.Time { return now }}
	got, err := f.Find(context.Background(), config.AutoWatchReviewRequested)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(numbers(got)) != "[1 2]" {
		t.Errorf("found %v, want PRs awaiting my review only", numbers(got))
	}

	now = now.Add(BitbucketReviewScanEvery - time.Second)
	f.Find(context.Background(), config.AutoWatchReviewRequested)
	if api.scans != 1 {
		t.Errorf("rescanned after %s: %d scans", BitbucketReviewScanEvery-time.Second, api.scans)
	}
	now = now.Add(time.Second)
	f.Find(context.Background(), config.AutoWatchReviewRequested)
	if api.scans != 2 {
		t.Errorf("no rescan after %s: %d scans", BitbucketReviewScanEvery, api.scans)
	}
}

func TestBitbucketFinder_FailedRepoMakesReviewScanPartial(t *testing.T) {
	api := &fakeLister{
		repos:   map[string][]string{"acme": {"api", "web"}},
		reviews: map[string][]bitbucket.ListedPR{"acme/api": {listed("acme", "api", 1)}},
		errs:    map[string]error{"acme/web": errors.New("boom")},
	}
	f := &BitbucketFinder{API: api, User: me, Workspaces: []string{"acme"}}
	got, err := f.Find(context.Background(), config.AutoWatchReviewRequested)
	if err != nil || got.Partial == "" {
		t.Errorf("got %+v, %v; a failed repository must make the scan partial", got, err)
	}
}

func TestSync_TwoHostsKeepTheirPRsApart(t *testing.T) {
	useTempConfig(t)
	saveConfig(t, &config.Config{
		AutoWatch: []string{config.AutoWatchMine, config.AutoWatchReviewRequested},
		WatchedPRs: []config.WatchedPR{
			// Same owner/repo#number on both hosts: only Bitbucket's review was withdrawn.
			{Owner: "acme", Repo: "api", Number: 7, Source: config.AutoWatchReviewRequested},
			{Host: forge.Bitbucket, Owner: "acme", Repo: "api", Number: 7, Source: config.AutoWatchReviewRequested},
		},
	})
	gh := &fakeSearcher{results: map[string]*github.SearchResult{
		config.AutoWatchMine:            found(1),
		config.AutoWatchReviewRequested: {PRs: []github.SearchResultPR{{Owner: "acme", Repo: "api", Number: 7}}, Total: 1},
	}}
	bbAPI := &fakeLister{
		mine:  map[string][]bitbucket.ListedPR{"acme": {listed("acme", "api", 1)}},
		repos: map[string][]string{"acme": {"api"}},
	}
	bbAPI.prs = map[string]*github.PullRequest{"acme/api/7": openReviewPR(7)}
	bb := &BitbucketFinder{API: bbAPI, User: me, Workspaces: []string{"acme"}}

	res, err := Sync(context.Background(), []Finder{GitHubFinder{Searcher: gh}, bb}, []string{config.AutoWatchMine, config.AutoWatchReviewRequested})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 2 || res.Added[0].Host != "" || res.Added[1].Host != forge.Bitbucket {
		t.Errorf("added = %+v, want #1 on each host", res.Added)
	}
	if len(res.Removed) != 1 || res.Removed[0].Host != forge.Bitbucket {
		t.Errorf("removed = %+v, want only Bitbucket's #7", res.Removed)
	}
	cfg, _ := config.Load()
	var names []string
	for _, pr := range cfg.WatchedPRs {
		names = append(names, DisplayPR(pr))
	}
	if fmt.Sprint(names) != "[acme/api#7 owner/repo#1 bitbucket:acme/api#1]" {
		t.Errorf("watch list = %v", names)
	}
}

func TestBitbucketInterval(t *testing.T) {
	for n, want := range map[int]time.Duration{0: time.Minute, 3: time.Minute, 5: 90 * time.Second, 20: 6 * time.Minute, 30: 9 * time.Minute, 100: 30 * time.Minute} {
		if got := BitbucketInterval(n); got != want {
			t.Errorf("BitbucketInterval(%d) = %s, want %s", n, got, want)
		}
	}
}
