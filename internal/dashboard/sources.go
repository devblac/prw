package dashboard

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/devblac/prw/internal/bitbucket"
	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
)

// ciAPI is what both sources need to read a commit's CI.
type ciAPI interface {
	GetCombinedStatus(ctx context.Context, owner, repo, ref string) (*github.CombinedStatus, error)
	GetCheckRuns(ctx context.Context, owner, repo, ref string) ([]github.CheckRun, error)
}

// readCI fills the item's CI fields from its head commit's checks and statuses.
func readCI(ctx context.Context, api ciAPI, it *Item) error {
	ci, err := github.FetchCIStatus(ctx, api, it.Ref.Owner, it.Ref.Repo, it.HeadSHA)
	if err != nil {
		return err
	}
	github.ApplyNoCI(ci, it.UpdatedAt, time.Now())
	it.CI, it.CITotal, it.CIDone = ci.State, ci.Total, ci.Total-ci.Pending
	it.FailedChecks = nil
	for _, f := range ci.FailedChecks {
		it.FailedChecks = append(it.FailedChecks, f.Name)
	}
	return nil
}

// GitHubAPI is what the GitHub source needs from the client.
type GitHubAPI interface {
	ciAPI
	SearchPullRequests(ctx context.Context, query string) (*github.SearchResult, error)
	GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error)
	GetReviews(ctx context.Context, owner, repo string, number int) ([]github.Review, error)
	RequestsInLastHour() int
}

// GitHubSource finds PRs with GitHub's search, across all repositories.
type GitHubSource struct {
	API GitHubAPI
	// Host is a GitHub Enterprise host; empty means github.com.
	Host string
}

func (s *GitHubSource) Name() string            { return forge.ServiceName(s.Host) }
func (s *GitHubSource) RequestsInLastHour() int { return s.API.RequestsInLastHour() }

// Discover runs one search per selected filter and reads each PR (branches, head,
// merge state); unchanged PRs answer 304, which is free.
func (s *GitHubSource) Discover(ctx context.Context, f Filter) ([]Item, []string, error) {
	type query struct{ q, role, state string }
	var queries []query
	if f.Mine {
		queries = append(queries, query{"is:pr is:open archived:false author:@me", RoleMine, StateOpen})
	}
	if f.Review {
		queries = append(queries, query{"is:pr is:open archived:false review-requested:@me", RoleReview, StateOpen})
	}
	if !f.MergedSince.IsZero() {
		queries = append(queries, query{"is:pr is:merged author:@me merged:>=" + f.MergedSince.UTC().Format("2006-01-02"), RoleMine, StateMerged})
	}

	var items []Item
	var warnings []string
	for _, q := range queries {
		res, err := s.API.SearchPullRequests(ctx, q.q)
		if err != nil {
			return nil, warnings, err
		}
		if res.Incomplete {
			warnings = append(warnings, fmt.Sprintf("search %q was incomplete", q.q))
		}
		found := make([]Item, len(res.PRs))
		errs := make([]error, len(res.PRs))
		var wg sync.WaitGroup
		sem := make(chan struct{}, enrichConcurrency)
		for i, r := range res.PRs {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, r github.SearchResultPR) {
				defer wg.Done()
				defer func() { <-sem }()
				pr, err := s.API.GetPullRequest(ctx, r.Owner, r.Repo, r.Number)
				if err != nil {
					errs[i] = err
					return
				}
				host := forge.NormalizeHost(s.Host)
				ref := forge.Ref{Host: host, Owner: r.Owner, Repo: r.Repo, Number: r.Number}
				found[i] = Item{
					Ref: ref, URL: ref.URL(), Host: host, Repo: r.Owner + "/" + r.Repo, Number: r.Number,
					Title: pr.Title, Source: pr.Head.Ref, Target: pr.Base.Ref, State: q.state, Role: q.role,
					UpdatedAt: pr.UpdatedAt, HeadSHA: pr.Head.SHA, Merge: github.MergeStatus(pr),
				}
			}(i, r)
		}
		wg.Wait()
		for i, it := range found {
			if errs[i] != nil {
				warnings = append(warnings, fmt.Sprintf("%s/%s#%d: %v", res.PRs[i].Owner, res.PRs[i].Repo, res.PRs[i].Number, errs[i]))
				continue
			}
			if q.state == StateMerged && it.UpdatedAt.Before(f.MergedSince) {
				continue
			}
			items = append(items, it)
		}
	}
	return items, warnings, nil
}

// Enrich reads CI (when needed) and reviews.
func (s *GitHubSource) Enrich(ctx context.Context, it *Item, needCI bool) error {
	if needCI {
		if err := readCI(ctx, s.API, it); err != nil {
			return err
		}
	}
	reviews, err := s.API.GetReviews(ctx, it.Ref.Owner, it.Ref.Repo, it.Number)
	if err != nil {
		return err
	}
	it.Review = github.ReviewDecision(reviews)
	it.Approvals = 0
	for _, r := range latestVerdicts(reviews) {
		if r == "APPROVED" {
			it.Approvals++
		}
	}
	return nil
}

// latestVerdicts returns each reviewer's latest approve / request-changes verdict.
func latestVerdicts(reviews []github.Review) map[string]string {
	v := map[string]string{}
	for _, r := range reviews {
		switch r.State {
		case "APPROVED", "CHANGES_REQUESTED":
			v[r.User.Login] = r.State
		case "DISMISSED":
			delete(v, r.User.Login)
		}
	}
	return v
}

// BitbucketAPI is what the Bitbucket source needs from the client.
type BitbucketAPI interface {
	ciAPI
	CurrentUser(ctx context.Context) (string, error)
	Workspaces(ctx context.Context) ([]string, error)
	UserPullRequests(ctx context.Context, workspace, user, state string, updatedSince time.Time) ([]bitbucket.ListedPR, error)
	RecentRepos(ctx context.Context, workspace string, since time.Time, max int) ([]string, error)
	ReviewPullRequests(ctx context.Context, workspace, repo, user string) ([]bitbucket.ListedPR, error)
	MergeStateWithin(ctx context.Context, workspace, repo string, number int, updatedOn time.Time, ttl time.Duration) string
	RequestsInLastHour() int
}

// Bitbucket discovery tuning: Bitbucket can only list a user's PRs per workspace and
// find PRs to review per repository, so the review scan is the expensive part.
const (
	DefaultReviewScanEvery = 10 * time.Minute
	reviewRepoWindow       = 30 * 24 * time.Hour // repositories active in the last 30 days
	maxReviewRepos         = 60
	dashboardMergeTTL      = 30 * time.Minute
)

// BitbucketSource finds PRs per workspace: yours with one request per workspace,
// those to review by scanning recently active repositories every ReviewScanEvery.
type BitbucketSource struct {
	API BitbucketAPI
	// User is a UUID or account id; empty means the token's owner.
	User string
	// Workspaces to look in; empty means all of the token owner's workspaces.
	Workspaces      []string
	ReviewScanEvery time.Duration
	Now             func() time.Time

	lastReviewScan time.Time
	reviewItems    []Item
}

func (s *BitbucketSource) Name() string            { return "Bitbucket" }
func (s *BitbucketSource) RequestsInLastHour() int { return s.API.RequestsInLastHour() }

func (s *BitbucketSource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *BitbucketSource) resolve(ctx context.Context) error {
	if s.User == "" {
		user, err := s.API.CurrentUser(ctx)
		if err != nil {
			return fmt.Errorf("finding out who you are needs a Bitbucket token (prw config set bitbucket_token), or set bitbucket_user: %w", err)
		}
		s.User = user
	}
	if len(s.Workspaces) == 0 {
		ws, err := s.API.Workspaces(ctx)
		if err != nil || len(ws) == 0 {
			if err == nil {
				err = fmt.Errorf("none found")
			}
			return fmt.Errorf("listing your workspaces failed; set them with 'prw config set bitbucket_workspaces ws1,ws2': %w", err)
		}
		s.Workspaces = ws
	}
	return nil
}

func (s *BitbucketSource) Discover(ctx context.Context, f Filter) ([]Item, []string, error) {
	if err := s.resolve(ctx); err != nil {
		return nil, nil, err
	}
	var items []Item
	var warnings []string
	for _, ws := range s.Workspaces {
		if f.Mine {
			prs, err := s.API.UserPullRequests(ctx, ws, s.User, "OPEN", time.Time{})
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", ws, err))
			}
			items = append(items, toItems(prs, RoleMine)...)
		}
		if !f.MergedSince.IsZero() {
			prs, err := s.API.UserPullRequests(ctx, ws, s.User, "MERGED", f.MergedSince)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s merged: %v", ws, err))
			}
			items = append(items, toItems(prs, RoleMine)...)
		}
	}
	if f.Review {
		every := s.ReviewScanEvery
		if every <= 0 {
			every = DefaultReviewScanEvery
		}
		if s.lastReviewScan.IsZero() || s.now().Sub(s.lastReviewScan) >= every {
			found, warns := s.scanReviews(ctx)
			warnings = append(warnings, warns...)
			s.reviewItems, s.lastReviewScan = found, s.now()
		}
		items = append(items, s.reviewItems...)
	}
	return items, warnings, nil
}

func (s *BitbucketSource) scanReviews(ctx context.Context) ([]Item, []string) {
	var items []Item
	var warnings []string
	for _, ws := range s.Workspaces {
		repos, err := s.API.RecentRepos(ctx, ws, s.now().Add(-reviewRepoWindow), maxReviewRepos)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s repositories: %v", ws, err))
			continue
		}
		for _, repo := range repos {
			prs, err := s.API.ReviewPullRequests(ctx, ws, repo, s.User)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s/%s reviews: %v", ws, repo, err))
				continue
			}
			items = append(items, toItems(prs, RoleReview)...)
		}
	}
	return items, warnings
}

func toItems(prs []bitbucket.ListedPR, role string) []Item {
	var items []Item
	for _, p := range prs {
		ref := forge.Ref{Host: forge.Bitbucket, Owner: p.Workspace, Repo: p.Repo, Number: p.Number}
		it := Item{
			Ref: ref, URL: ref.URL(), Host: forge.Bitbucket, Repo: p.Workspace + "/" + p.Repo, Number: p.Number,
			Title: p.Title, Source: p.Source, Target: p.Target, Role: role, UpdatedAt: p.UpdatedOn, HeadSHA: p.SHA,
			Approvals: p.Approvals,
		}
		switch p.State {
		case "OPEN":
			it.State = StateOpen
		case "MERGED":
			it.State = StateMerged
		default:
			it.State = StateClosed
		}
		switch {
		case p.ChangesRequested:
			it.Review = "changes_requested"
		case p.Approvals > 0:
			it.Review = "approved"
		default:
			it.Review = "none"
		}
		items = append(items, it)
	}
	return items
}

// Enrich reads CI (when needed) and the merge state (cached for 30 minutes while
// the PR is unchanged). Approvals came with the list.
func (s *BitbucketSource) Enrich(ctx context.Context, it *Item, needCI bool) error {
	if needCI {
		if err := readCI(ctx, s.API, it); err != nil {
			return err
		}
	}
	switch s.API.MergeStateWithin(ctx, it.Ref.Owner, it.Ref.Repo, it.Number, it.UpdatedAt, dashboardMergeTTL) {
	case "dirty":
		it.Merge = "conflict"
	case "blocked":
		it.Merge = "blocked"
	case "clean":
		it.Merge = "ready"
	}
	return nil
}
