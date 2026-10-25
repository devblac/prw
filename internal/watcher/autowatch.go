package watcher

import (
	"fmt"
	"strings"
	"time"

	"github.com/devblac/prw/internal/config"
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

// Searcher runs GitHub pull request searches.
type Searcher interface {
	SearchPullRequests(query string) (*github.SearchResult, error)
}

// SyncResult reports what SyncAutoWatch changed.
type SyncResult struct {
	Added   []config.WatchedPR
	Removed []config.WatchedPR
	// Warnings are non-fatal problems, e.g. a search that failed or was truncated.
	Warnings []string
}

// SyncAutoWatch runs the search of every source in sources and updates the watch list:
// matching PRs that aren't watched yet are added (tagged with their source), and PRs
// added for "review-requested" that no longer match are removed, since the request
// was withdrawn or answered. PRs added for "mine" are not removed when they stop
// matching: that means they were merged or closed, and `prw run` should still notify
// about it before dropping them. PRs watched by hand are never touched.
//
// It returns an error only if every search failed; other problems are Warnings.
func SyncAutoWatch(client Searcher, sources []string) (*SyncResult, error) {
	result := &SyncResult{}
	found := map[string]*github.SearchResult{}
	var lastErr error

	for _, source := range sources {
		query, ok := autoWatchQueries[source]
		if !ok {
			result.Warnings = append(result.Warnings, fmt.Sprintf("unknown auto-watch source %q ignored", source))
			continue
		}
		res, err := client.SearchPullRequests(query)
		if err != nil {
			lastErr = err
			result.Warnings = append(result.Warnings, fmt.Sprintf("search for %s failed: %v", AutoWatchDescription[source], err))
			continue
		}
		if res.Incomplete {
			result.Warnings = append(result.Warnings, fmt.Sprintf("search for %s returned %d of %d results; PRs that no longer match are kept",
				AutoWatchDescription[source], len(res.PRs), res.Total))
		}
		found[source] = res
	}
	if len(found) == 0 && lastErr != nil {
		return result, lastErr
	}

	err := config.Update(func(c *config.Config) error {
		changed := false
		for _, source := range sources {
			res, ok := found[source]
			if !ok || !c.HasAutoWatch(source) {
				continue // search failed, or the source was disabled meanwhile
			}

			matches := make(map[string]bool, len(res.PRs))
			for _, pr := range res.PRs {
				matches[prKey(pr.Owner, pr.Repo, pr.Number)] = true
				added := config.WatchedPR{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number, Title: pr.Title, Source: source}
				if c.AddPR(added) {
					result.Added = append(result.Added, added)
					changed = true
				}
			}

			if source != config.AutoWatchReviewRequested || res.Incomplete {
				continue
			}
			kept := c.WatchedPRs[:0]
			for _, pr := range c.WatchedPRs {
				if pr.Source == source && !matches[prKey(pr.Owner, pr.Repo, pr.Number)] {
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

func prKey(owner, repo string, number int) string {
	return strings.ToLower(owner) + "/" + strings.ToLower(repo) + "#" + fmt.Sprint(number)
}

// syncAutoWatch refreshes the auto-watch searches during `prw run` and reports changes.
func (w *Watcher) syncAutoWatch() {
	sources := w.config.AutoWatch
	if fresh, err := config.Load(); err == nil {
		sources = fresh.AutoWatch // `prw watch --mine` may have run since startup
	}
	if len(sources) == 0 {
		return
	}
	w.lastAutoWatch = w.now()

	searcher, ok := w.client.(Searcher)
	if !ok {
		return
	}
	res, err := SyncAutoWatch(searcher, sources)
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
		fmt.Printf("➕ Auto-watching %s/%s#%d - %s (%s)\n", pr.Owner, pr.Repo, pr.Number, pr.Title, AutoWatchDescription[pr.Source])
	}
	for _, pr := range res.Removed {
		fmt.Printf("➖ No longer watching %s/%s#%d - review no longer requested\n", pr.Owner, pr.Repo, pr.Number)
	}
}
