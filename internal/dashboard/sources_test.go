package dashboard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/devblac/prw/internal/bitbucket"
	"github.com/devblac/prw/internal/github"
)

// fakeBitbucket implements BitbucketAPI.
type fakeBitbucket struct {
	user, userErr    string
	workspaces       []string
	open, merged     map[string][]bitbucket.ListedPR // by workspace
	repos            []string
	review           map[string][]bitbucket.ListedPR // by repo
	merge            string
	runs             []github.CheckRun
	repoScans        int
	mergedSinceAsked time.Time
}

func (f *fakeBitbucket) CurrentUser(ctx context.Context) (string, error) {
	if f.userErr != "" {
		return "", errors.New(f.userErr)
	}
	return f.user, nil
}
func (f *fakeBitbucket) Workspaces(ctx context.Context) ([]string, error) { return f.workspaces, nil }
func (f *fakeBitbucket) UserPullRequests(ctx context.Context, ws, user, state string, since time.Time) ([]bitbucket.ListedPR, error) {
	if user != "{me}" {
		return nil, fmt.Errorf("unexpected user %s", user)
	}
	if state == "MERGED" {
		f.mergedSinceAsked = since
		return f.merged[ws], nil
	}
	return f.open[ws], nil
}
func (f *fakeBitbucket) RecentRepos(ctx context.Context, ws string, since time.Time, max int) ([]string, error) {
	f.repoScans++
	return f.repos, nil
}
func (f *fakeBitbucket) ReviewPullRequests(ctx context.Context, ws, repo, user string) ([]bitbucket.ListedPR, error) {
	return f.review[repo], nil
}
func (f *fakeBitbucket) MergeStateWithin(ctx context.Context, ws, repo string, n int, updated time.Time, ttl time.Duration) string {
	return f.merge
}
func (f *fakeBitbucket) GetCombinedStatus(ctx context.Context, o, r, ref string) (*github.CombinedStatus, error) {
	return &github.CombinedStatus{State: "pending"}, nil
}
func (f *fakeBitbucket) GetCheckRuns(ctx context.Context, o, r, ref string) ([]github.CheckRun, error) {
	return f.runs, nil
}
func (f *fakeBitbucket) RequestsInLastHour() int { return 3 }

func listed(ws, repo string, n int, source, target, state string) bitbucket.ListedPR {
	return bitbucket.ListedPR{Workspace: ws, Repo: repo, Number: n, Source: source, Target: target, State: state, SHA: "sha", Title: "T"}
}

func TestBitbucketSource_Discover(t *testing.T) {
	approved := listed("sec", "api", 2, "SEC-1-rc", "rc", "OPEN")
	approved.Approvals = 2
	changes := listed("sec", "web", 3, "SEC-2", "dev", "OPEN")
	changes.ChangesRequested = true
	api := &fakeBitbucket{
		user: "{me}", workspaces: []string{"sec"},
		open:   map[string][]bitbucket.ListedPR{"sec": {listed("sec", "api", 1, "SEC-1", "dev", "OPEN"), approved, changes}},
		merged: map[string][]bitbucket.ListedPR{"sec": {listed("sec", "api", 9, "SEC-0", "master", "MERGED")}},
		repos:  []string{"api", "web"},
		review: map[string][]bitbucket.ListedPR{"web": {listed("sec", "web", 5, "SEC-7", "dev", "OPEN")}},
	}
	src := &BitbucketSource{API: api}
	since := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	items, warnings, err := src.Discover(context.Background(), Filter{Mine: true, Review: true, MergedSince: since})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("err=%v warnings=%v", err, warnings)
	}
	var got []string
	for _, it := range items {
		got = append(got, fmt.Sprintf("%s#%d:%s:%s:%s:%d", it.Repo, it.Number, it.State, it.Role, it.Review, it.Approvals))
	}
	want := "sec/api#1:open:mine:none:0 sec/api#2:open:mine:approved:2 sec/web#3:open:mine:changes_requested:0 sec/api#9:merged:mine:none:0 sec/web#5:open:review:none:0"
	if strings.Join(got, " ") != want {
		t.Errorf("items = %s\nwant    %s", strings.Join(got, " "), want)
	}
	if !api.mergedSinceAsked.Equal(since) {
		t.Errorf("merged since = %v", api.mergedSinceAsked)
	}
	if items[0].URL != "https://bitbucket.org/sec/api/pull-requests/1" || items[0].Target != "dev" {
		t.Errorf("item = %+v", items[0])
	}
}

func TestBitbucketSource_ReviewScanIsThrottled(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	api := &fakeBitbucket{user: "{me}", workspaces: []string{"sec"}, repos: []string{"api"},
		review: map[string][]bitbucket.ListedPR{"api": {listed("sec", "api", 5, "x", "dev", "OPEN")}}}
	src := &BitbucketSource{API: api, Now: func() time.Time { return now }}

	for i := 0; i < 3; i++ {
		items, _, _ := src.Discover(context.Background(), Filter{Review: true})
		if len(items) != 1 {
			t.Fatalf("refresh %d: the last scan's PRs must still be shown: %d", i, len(items))
		}
		now = now.Add(time.Minute)
	}
	if api.repoScans != 1 {
		t.Errorf("repositories scanned %d times in 3 minutes, want once per %s", api.repoScans, DefaultReviewScanEvery)
	}
	now = now.Add(DefaultReviewScanEvery)
	src.Discover(context.Background(), Filter{Review: true})
	if api.repoScans != 2 {
		t.Errorf("scans = %d, want a new one after %s", api.repoScans, DefaultReviewScanEvery)
	}
}

func TestBitbucketSource_NeedsTokenOrUser(t *testing.T) {
	src := &BitbucketSource{API: &fakeBitbucket{userErr: "401"}}
	if _, _, err := src.Discover(context.Background(), Filter{Mine: true}); err == nil || !strings.Contains(err.Error(), "bitbucket_token") {
		t.Errorf("err = %v", err)
	}
	src = &BitbucketSource{API: &fakeBitbucket{}, User: "{me}"}
	if _, _, err := src.Discover(context.Background(), Filter{Mine: true}); err == nil || !strings.Contains(err.Error(), "bitbucket_workspaces") {
		t.Errorf("err = %v", err)
	}
}

func TestBitbucketSource_Enrich(t *testing.T) {
	api := &fakeBitbucket{merge: "dirty", runs: []github.CheckRun{
		{Name: "Pipeline", Status: "completed", Conclusion: "success"},
		{Name: "Jenkins", Status: "in_progress"},
	}}
	src := &BitbucketSource{API: api}
	it := toItems([]bitbucket.ListedPR{listed("sec", "api", 1, "x", "dev", "OPEN")}, RoleMine)[0]
	if err := src.Enrich(context.Background(), &it, true); err != nil {
		t.Fatal(err)
	}
	if it.CI != "pending" || it.CIDone != 1 || it.CITotal != 2 || it.Merge != "conflict" {
		t.Errorf("item = %+v", it)
	}
}

// fakeGitHub implements GitHubAPI.
type fakeGitHub struct {
	results map[string][]github.SearchResultPR // by role keyword
	prs     map[int]*github.PullRequest
	reviews []github.Review
}

func (f *fakeGitHub) SearchPullRequests(ctx context.Context, q string) (*github.SearchResult, error) {
	// Most specific keyword first: the merged query contains "author:@me" too.
	for _, kw := range []string{"author:@me merged:", "review-requested", "author:@me"} {
		if prs, ok := f.results[kw]; ok && strings.Contains(q, kw) {
			return &github.SearchResult{PRs: prs, Total: len(prs)}, nil
		}
	}
	return &github.SearchResult{}, nil
}
func (f *fakeGitHub) GetPullRequest(ctx context.Context, o, r string, n int) (*github.PullRequest, error) {
	if pr, ok := f.prs[n]; ok {
		return pr, nil
	}
	return nil, errors.New("404")
}
func (f *fakeGitHub) GetReviews(ctx context.Context, o, r string, n int) ([]github.Review, error) {
	return f.reviews, nil
}
func (f *fakeGitHub) GetCombinedStatus(ctx context.Context, o, r, ref string) (*github.CombinedStatus, error) {
	return &github.CombinedStatus{State: "pending"}, nil
}
func (f *fakeGitHub) GetCheckRuns(ctx context.Context, o, r, ref string) ([]github.CheckRun, error) {
	return []github.CheckRun{{Name: "Test", Status: "completed", Conclusion: "success"}}, nil
}
func (f *fakeGitHub) RequestsInLastHour() int { return 11 }

func ghPR(n int, head, base, state string, updated time.Time) *github.PullRequest {
	pr := &github.PullRequest{Number: n, Title: "T", State: state, UpdatedAt: updated, MergeableState: "clean"}
	pr.Head.SHA, pr.Head.Ref, pr.Base.Ref = "sha", head, base
	return pr
}

func TestGitHubSource(t *testing.T) {
	since := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	api := &fakeGitHub{
		results: map[string][]github.SearchResultPR{
			"author:@me merged:": {{Owner: "o", Repo: "r", Number: 3}, {Owner: "o", Repo: "r", Number: 4}},
			"author:@me":         {{Owner: "o", Repo: "r", Number: 1}, {Owner: "o", Repo: "gone", Number: 99}},
			"review-requested":   {{Owner: "o", Repo: "r", Number: 2}},
		},
		prs: map[int]*github.PullRequest{
			1: ghPR(1, "A-1/x", "dev", "open", since), 2: ghPR(2, "B-2/y", "main", "open", since),
			3: ghPR(3, "A-0/z", "main", "closed", since.Add(time.Hour)),
			4: ghPR(4, "old", "main", "closed", since.Add(-time.Hour)), // merged before the window
		},
		reviews: []github.Review{
			{State: "APPROVED", User: struct {
				Login string `json:"login"`
			}{"ana"}},
		},
	}
	src := &GitHubSource{API: api}
	items, warnings, err := src.Discover(context.Background(), Filter{Mine: true, Review: true, MergedSince: since})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, fmt.Sprintf("#%d:%s:%s:%s->%s:%s", it.Number, it.State, it.Role, it.Source, it.Target, it.Merge))
	}
	if want := "#1:open:mine:A-1/x->dev:ready #2:open:review:B-2/y->main:ready #3:merged:mine:A-0/z->main:ready"; strings.Join(got, " ") != want {
		t.Errorf("items = %s\nwant    %s", strings.Join(got, " "), want)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "o/gone#99") {
		t.Errorf("warnings = %v", warnings)
	}

	it := items[0]
	if err := src.Enrich(context.Background(), &it, true); err != nil {
		t.Fatal(err)
	}
	if it.CI != "success" || it.Review != "approved" || it.Approvals != 1 || it.CITotal != 1 {
		t.Errorf("enriched = %+v", it)
	}
}
