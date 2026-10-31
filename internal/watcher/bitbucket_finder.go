package watcher

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devblac/prw/internal/bitbucket"
	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
)

// BitbucketLister is what BitbucketFinder needs from the Bitbucket client.
type BitbucketLister interface {
	CurrentUser(ctx context.Context) (string, error)
	Workspaces(ctx context.Context) ([]string, error)
	UserPullRequests(ctx context.Context, workspace, user, state string, updatedSince time.Time) ([]bitbucket.ListedPR, error)
	RecentRepos(ctx context.Context, workspace string, since time.Time, max int) ([]string, error)
	ReviewPullRequests(ctx context.Context, workspace, repo, user string) ([]bitbucket.ListedPR, error)
	GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error)
}

// Bitbucket can only list a user's PRs per workspace, and find PRs to review per
// repository, so the review scan is the expensive part: it covers the repositories
// active in the last 30 days (at most 60 per workspace) and runs every 15 minutes.
const (
	BitbucketReviewScanEvery = 15 * time.Minute
	bitbucketReviewWindow    = 30 * 24 * time.Hour
	bitbucketMaxReviewRepos  = 60
)

// BitbucketFinder finds your open PRs and the open PRs you are a reviewer of (and
// haven't approved or requested changes on yet) across your Bitbucket workspaces.
type BitbucketFinder struct {
	API BitbucketLister
	// User is a UUID or account id; empty means the token's owner.
	User string
	// Workspaces to look in; empty means all of the token owner's workspaces.
	Workspaces []string
	Now        func() time.Time

	lastReviewScan time.Time
	reviewFound    *Found
}

func (f *BitbucketFinder) Host() string { return forge.Bitbucket }

func (f *BitbucketFinder) GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error) {
	return f.API.GetPullRequest(ctx, owner, repo, number)
}

func (f *BitbucketFinder) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *BitbucketFinder) resolve(ctx context.Context) error {
	if f.User == "" {
		user, err := f.API.CurrentUser(ctx)
		if err != nil {
			return fmt.Errorf("finding out who you are needs a Bitbucket token (prw config set bitbucket_token), or set bitbucket_user: %w", err)
		}
		f.User = user
	}
	if len(f.Workspaces) == 0 {
		ws, err := f.API.Workspaces(ctx)
		if err == nil && len(ws) == 0 {
			err = errors.New("none found")
		}
		if err != nil {
			return fmt.Errorf("listing your workspaces failed; set them with 'prw config set bitbucket_workspaces ws1,ws2': %w", err)
		}
		f.Workspaces = ws
	}
	return nil
}

func (f *BitbucketFinder) Find(ctx context.Context, source string) (*Found, error) {
	if err := f.resolve(ctx); err != nil {
		return nil, err
	}
	switch source {
	case config.AutoWatchMine:
		return f.mine(ctx)
	case config.AutoWatchReviewRequested:
		if f.reviewFound == nil || f.now().Sub(f.lastReviewScan) >= BitbucketReviewScanEvery {
			found, err := f.scanReviews(ctx)
			if err != nil {
				return nil, err
			}
			f.reviewFound, f.lastReviewScan = found, f.now()
		}
		return f.reviewFound, nil
	}
	return nil, fmt.Errorf("unknown auto-watch source %q", source)
}

func (f *BitbucketFinder) mine(ctx context.Context) (*Found, error) {
	found := &Found{}
	var failed []string
	var lastErr error
	for _, ws := range f.Workspaces {
		prs, err := f.API.UserPullRequests(ctx, ws, f.User, "OPEN", time.Time{})
		if err != nil {
			failed, lastErr = append(failed, ws), err
			continue
		}
		found.PRs = append(found.PRs, toSearchPRs(prs, nil)...)
	}
	return partial(found, failed, len(f.Workspaces), lastErr)
}

func (f *BitbucketFinder) scanReviews(ctx context.Context) (*Found, error) {
	found := &Found{}
	var failed []string
	var lastErr error
	scanned := 0
	for _, ws := range f.Workspaces {
		repos, err := f.API.RecentRepos(ctx, ws, f.now().Add(-bitbucketReviewWindow), bitbucketMaxReviewRepos)
		scanned++
		if err != nil {
			failed, lastErr = append(failed, ws), err
			continue
		}
		for _, repo := range repos {
			prs, err := f.API.ReviewPullRequests(ctx, ws, repo, f.User)
			scanned++
			if err != nil {
				failed, lastErr = append(failed, ws+"/"+repo), err
				continue
			}
			found.PRs = append(found.PRs, toSearchPRs(prs, func(p bitbucket.ListedPR) bool { return !f.reviewed(p) })...)
		}
		if len(repos) == bitbucketMaxReviewRepos && found.Partial == "" {
			found.Partial = fmt.Sprintf("covered only the %d most recently active repositories of %s", bitbucketMaxReviewRepos, ws)
		}
	}
	return partial(found, failed, scanned, lastErr)
}

// reviewed reports whether the user already approved or requested changes on p: like
// on GitHub, the review is then no longer pending.
func (f *BitbucketFinder) reviewed(p bitbucket.ListedPR) bool {
	for _, id := range p.ReviewedBy {
		if id != "" && strings.EqualFold(id, f.User) {
			return true
		}
	}
	return false
}

// partial fails when every request failed, and otherwise notes what was missed.
func partial(found *Found, failed []string, total int, lastErr error) (*Found, error) {
	if len(failed) == 0 {
		return found, nil
	}
	if len(failed) == total {
		return nil, lastErr
	}
	if found.Partial == "" {
		found.Partial = fmt.Sprintf("failed for %s (%v)", strings.Join(failed, ", "), lastErr)
	}
	return found, nil
}

func toSearchPRs(prs []bitbucket.ListedPR, keep func(bitbucket.ListedPR) bool) []github.SearchResultPR {
	var out []github.SearchResultPR
	for _, p := range prs {
		if keep != nil && !keep(p) {
			continue
		}
		out = append(out, github.SearchResultPR{Owner: p.Workspace, Repo: p.Repo, Number: p.Number, Title: p.Title})
	}
	return out
}
