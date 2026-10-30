package dashboard

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Filter selects which PRs the dashboard shows.
type Filter struct {
	Mine   bool
	Review bool
	// MergedSince, if not zero, adds your PRs merged since then.
	MergedSince time.Time
}

// Source is a code host the dashboard reads PRs from.
type Source interface {
	Name() string
	// Discover lists the PRs matching f, with whatever the list provides (for
	// Bitbucket, approvals too). Warnings are non-fatal problems.
	Discover(ctx context.Context, f Filter) (items []Item, warnings []string, err error)
	// Enrich fills in an open item's CI (only if needCI), reviews and merge state.
	Enrich(ctx context.Context, it *Item, needCI bool) error
	// RequestsInLastHour is how many API requests the source made in the last hour.
	RequestsInLastHour() int
}

// DefaultCIRecheck is how often the CI of a PR whose checks have all finished is
// read again although its head didn't change (e.g. someone re-ran a build).
const DefaultCIRecheck = 15 * time.Minute

// enrichConcurrency bounds requests in flight while enriching items.
const enrichConcurrency = 4

// Collector refreshes the dashboard's items, re-reading a PR's CI only when it may
// have changed: new head commit, checks still running, or DefaultCIRecheck elapsed.
// That keeps a refresh every minute within Bitbucket's 1,000 requests/hour for
// dozens of PRs.
type Collector struct {
	Sources   []Source
	Now       func() time.Time
	CIRecheck time.Duration

	mu    sync.Mutex
	ci    map[string]ciSnapshot
	hosts map[string]Source // item host → source, for Enrich
}

type ciSnapshot struct {
	sha     string
	item    Item // CI fields only
	readAt  time.Time
	running bool
}

// NewCollector returns a collector over sources.
func NewCollector(sources ...Source) *Collector {
	return &Collector{Sources: sources, Now: time.Now, CIRecheck: DefaultCIRecheck, ci: map[string]ciSnapshot{}}
}

// Refresh discovers the PRs of every source and enriches the open ones. Items are
// returned even if some requests failed; problems are reported as warnings.
func (c *Collector) Refresh(ctx context.Context, f Filter) ([]Item, []string) {
	var items []Item
	var warnings []string
	sourceOf := map[string]Source{}
	for _, src := range c.Sources {
		found, warns, err := src.Discover(ctx, f)
		for _, w := range warns {
			warnings = append(warnings, src.Name()+": "+w)
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", src.Name(), err))
			continue
		}
		for _, it := range found {
			sourceOf[it.Key()] = src
		}
		items = append(items, dedupe(found)...)
	}

	var wg sync.WaitGroup
	var wmu sync.Mutex
	sem := make(chan struct{}, enrichConcurrency)
	for i := range items {
		it := &items[i]
		if it.State != StateOpen {
			continue
		}
		needCI := c.needsCI(*it)
		if !needCI {
			c.applyCachedCI(it)
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(it *Item, src Source, needCI bool) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := src.Enrich(ctx, it, needCI); err != nil {
				wmu.Lock()
				warnings = append(warnings, fmt.Sprintf("%s %s: %v", src.Name(), it.Ref, err))
				wmu.Unlock()
				if needCI {
					c.applyCachedCI(it) // keep showing the last known CI
				}
				return
			}
			if needCI {
				c.storeCI(*it)
			}
		}(it, sourceOf[it.Key()], needCI)
	}
	wg.Wait()
	sort.Strings(warnings)
	return items, warnings
}

// dedupe drops repeated PRs (e.g. both yours and to review), keeping the first.
func dedupe(items []Item) []Item {
	seen := map[string]bool{}
	out := items[:0]
	for _, it := range items {
		if seen[it.Key()] {
			continue
		}
		seen[it.Key()] = true
		out = append(out, it)
	}
	return out
}

func (c *Collector) needsCI(it Item) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap, ok := c.ci[it.Key()]
	switch {
	case !ok, snap.sha != it.HeadSHA, snap.running:
		return true
	}
	return c.Now().Sub(snap.readAt) >= c.CIRecheck
}

func (c *Collector) applyCachedCI(it *Item) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if snap, ok := c.ci[it.Key()]; ok && snap.sha == it.HeadSHA {
		it.CI, it.CIDone, it.CITotal, it.FailedChecks = snap.item.CI, snap.item.CIDone, snap.item.CITotal, snap.item.FailedChecks
	}
}

func (c *Collector) storeCI(it Item) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ci[it.Key()] = ciSnapshot{sha: it.HeadSHA, item: it, readAt: c.Now(), running: it.CI == "pending" || it.CI == ""}
}

// Requests reports each source's requests in the last hour, e.g. "Bitbucket 312/h".
func (c *Collector) Requests() []string {
	var out []string
	for _, src := range c.Sources {
		out = append(out, fmt.Sprintf("%s %d/h", src.Name(), src.RequestsInLastHour()))
	}
	return out
}
