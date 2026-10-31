package watcher

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
)

// AutoWatchInterval is how often `prw run` refreshes the auto-watch searches. Search
// results change slowly and the search API has its own, lower rate limit (30/min), so
// this is much longer than the CI poll interval.
const AutoWatchInterval = 5 * time.Minute

// autoWatchQueries maps each auto-watch source to its GitHub search query.
var autoWatchQueries = map[string]string{
	config.AutoWatchMine:            "is:pr is:open archived:false author:@me",
	config.AutoWatchReviewRequested: "is:pr is:open archived:false review-requested:@me",
}

// AutoWatchDescription is a human-readable name for each source.
var AutoWatchDescription = map[string]string{
	config.AutoWatchMine:            "your open PRs",
	config.AutoWatchReviewRequested: "PRs where your review is requested",
}

// Searcher runs GitHub pull request searches. GetPullRequest tells a review request
// that was withdrawn apart from a PR that was merged or closed.
type Searcher interface {
	SearchPullRequests(ctx context.Context, query string) (*github.SearchResult, error)
	GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error)
}

// Found is the result of an auto-watch source on one host.
type Found struct {
	PRs []github.SearchResultPR
	// Partial says why some PRs may be missing (a truncated search, a workspace that
	// failed); PRs that no longer match are then kept.
	Partial string
}

// Finder finds the open PRs of an auto-watch source on one code host.
type Finder interface {
	Host() string // forge.GitHub, forge.Bitbucket
	Find(ctx context.Context, source string) (*Found, error)
	GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error)
}

// GitHubFinder finds PRs with GitHub's search.
type GitHubFinder struct {
	Searcher
}

func (GitHubFinder) Host() string { return forge.GitHub }

func (f GitHubFinder) Find(ctx context.Context, source string) (*Found, error) {
	query, ok := autoWatchQueries[source]
	if !ok {
		return nil, fmt.Errorf("unknown auto-watch source %q", source)
	}
	res, err := f.SearchPullRequests(ctx, query)
	if err != nil {
		return nil, err
	}
	found := &Found{PRs: res.PRs}
	if res.Incomplete {
		found.Partial = fmt.Sprintf("returned %d of %d results", len(res.PRs), res.Total)
	}
	return found, nil
}

// SyncResult reports what SyncAutoWatch changed.
type SyncResult struct {
	Added   []config.WatchedPR
	Removed []config.WatchedPR
	// Warnings are non-fatal problems, e.g. a search that failed or was truncated.
	Warnings []string
}

// SyncAutoWatch runs GitHub's search for every source in sources and updates the watch
// list; see Sync.
func SyncAutoWatch(ctx context.Context, client Searcher, sources []string) (*SyncResult, error) {
	return Sync(ctx, []Finder{GitHubFinder{client}}, sources)
}

// Sync runs every source in sources on every finder's host and updates the watch list:
// matching PRs that aren't watched yet are added (tagged with their source and host),
// and PRs added for "review-requested" that no longer match are removed, since the
// request was withdrawn or answered. PRs that stopped matching because they were
// merged or closed are kept instead (for "mine" that's always the reason), so that
// `prw run` still notifies about it before dropping them. PRs watched by hand are never
// touched.
//
// It returns an error if every search failed or ctx was cancelled (then nothing is
// changed); other problems are Warnings.
func Sync(ctx context.Context, finders []Finder, sources []string) (*SyncResult, error) {
	result := &SyncResult{}
	type search struct {
		finder Finder
		source string
		found  *Found
	}
	var searches []search
	var lastErr error

	for _, source := range sources {
		if _, ok := AutoWatchDescription[source]; !ok {
			result.Warnings = append(result.Warnings, fmt.Sprintf("unknown auto-watch source %q ignored", source))
			continue
		}
		for _, f := range finders {
			found, err := f.Find(ctx, source)
			if err != nil {
				lastErr = err
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s search for %s failed: %v", serviceName(f.Host()), AutoWatchDescription[source], err))
				continue
			}
			if found.Partial != "" {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s search for %s %s; PRs that no longer match are kept",
					serviceName(f.Host()), AutoWatchDescription[source], found.Partial))
			}
			searches = append(searches, search{f, source, found})
		}
	}
	if len(searches) == 0 && lastErr != nil {
		return result, lastErr
	}
	withdrawn := map[string]bool{}
	for _, s := range searches {
		if s.source == config.AutoWatchReviewRequested && s.found.Partial == "" {
			withdrawnReviewRequests(ctx, s.finder, s.found, withdrawn)
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err // interrupted: lookups that didn't finish prove nothing
	}

	err := config.Update(func(c *config.Config) error {
		changed := false
		for _, s := range searches {
			if !c.HasAutoWatch(s.source) {
				continue // the source was disabled meanwhile
			}
			host := s.finder.Host()
			matches := make(map[string]bool, len(s.found.PRs))
			for _, pr := range s.found.PRs {
				matches[prKey(host, pr.Owner, pr.Repo, pr.Number)] = true
				added := config.WatchedPR{Host: forge.StorageHost(host), Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number, Title: pr.Title, Source: s.source}
				if c.AddPR(added) {
					result.Added = append(result.Added, added)
					changed = true
				}
			}

			if s.source != config.AutoWatchReviewRequested || s.found.Partial != "" {
				continue
			}
			kept := c.WatchedPRs[:0]
			for _, pr := range c.WatchedPRs {
				key := prKey(pr.Host, pr.Owner, pr.Repo, pr.Number)
				if pr.Source == s.source && forge.NormalizeHost(pr.Host) == host && !matches[key] && withdrawn[key] {
					result.Removed = append(result.Removed, pr)
					changed = true
					continue
				}
				kept = append(kept, pr)
			}
			c.WatchedPRs = kept
		}
		if !changed {
			return config.ErrNoChanges
		}
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("failed to update watch list: %w", err)
	}
	return result, nil
}

// withdrawnReviewRequests marks in withdrawn the review-request PRs on f's host that
// can be dropped: they no longer match their search and are still open (so the request
// was withdrawn or answered), or are gone (404). The search only returns open PRs, so a
// merged or closed PR looks the same until it's looked up; those are kept for the
// regular check to notify about. A failed lookup (network, rate limit, cancellation)
// proves nothing, so that PR is kept until the next sync. It runs before the config is
// locked, as it makes API calls.
func withdrawnReviewRequests(ctx context.Context, f Finder, found *Found, withdrawn map[string]bool) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	host := f.Host()
	matches := make(map[string]bool, len(found.PRs))
	for _, pr := range found.PRs {
		matches[prKey(host, pr.Owner, pr.Repo, pr.Number)] = true
	}
	for _, pr := range cfg.WatchedPRs {
		key := prKey(pr.Host, pr.Owner, pr.Repo, pr.Number)
		if pr.Source != config.AutoWatchReviewRequested || forge.NormalizeHost(pr.Host) != host || matches[key] {
			continue
		}
		ghPR, err := f.GetPullRequest(ctx, pr.Owner, pr.Repo, pr.Number)
		var apiErr *github.APIError
		switch {
		case err == nil:
			withdrawn[key] = !ghPR.IsClosed()
		case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound:
			withdrawn[key] = true // deleted, or no longer visible to us
		}
	}
}

func prKey(host, owner, repo string, number int) string {
	return forge.NormalizeHost(host) + ":" + strings.ToLower(owner) + "/" + strings.ToLower(repo) + "#" + fmt.Sprint(number)
}

// AddFinder adds an auto-watch finder for a host other than GitHub (GitHub's is the
// GitHub client).
func (w *Watcher) AddFinder(f Finder) {
	w.finders = append(w.finders, f)
}

// autoWatchFinders returns the finders auto-watch runs on.
func (w *Watcher) autoWatchFinders() []Finder {
	var finders []Finder
	if s, ok := w.client.(Searcher); ok && !isNilClient(w.client) {
		finders = append(finders, GitHubFinder{s})
	}
	return append(finders, w.finders...)
}

// DisplayPR names a watched PR: owner/repo#N, prefixed with "bitbucket:" for
// Bitbucket.
func DisplayPR(pr config.WatchedPR) string {
	name := fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number)
	if forge.IsBitbucket(pr.Host) {
		name = "bitbucket:" + name
	}
	return name
}

// syncAutoWatch refreshes the auto-watch searches during `prw run` and reports changes.
func (w *Watcher) syncAutoWatch(ctx context.Context) {
	sources := w.config.AutoWatch
	if fresh, err := config.Load(); err == nil {
		sources = fresh.AutoWatch // `prw watch --mine` may have run since startup
	}
	if len(sources) == 0 {
		return
	}
	w.lastAutoWatch = w.now()

	finders := w.autoWatchFinders()
	if len(finders) == 0 {
		return
	}
	res, err := Sync(ctx, finders, sources)
	if ctx.Err() != nil {
		return // stopping; the failed searches and lookups aren't worth reporting
	}
	for _, warning := range res.Warnings {
		fmt.Printf("Warning: auto-watch: %s\n", warning)
	}
	if err != nil {
		// Per-search failures (including rate limits) are already in Warnings; this is
		// either "every search failed" or a config write error.
		if limited, _ := github.IsRateLimited(err); !limited {
			fmt.Printf("Warning: auto-watch failed: %v\n", err)
		}
		return
	}
	for _, pr := range res.Added {
		fmt.Printf("➕ Auto-watching %s - %s (%s)\n", DisplayPR(pr), pr.Title, AutoWatchDescription[pr.Source])
	}
	for _, pr := range res.Removed {
		fmt.Printf("➖ No longer watching %s - review no longer requested\n", DisplayPR(pr))
	}
}
