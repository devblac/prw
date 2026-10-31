package watcher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
	"github.com/devblac/prw/internal/notify"
)

// GitHubClient is what the watcher needs from a code host's client. The Bitbucket
// client implements it too, answering in the github package's types.
type GitHubClient interface {
	GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error)
	GetCombinedStatus(ctx context.Context, owner, repo, ref string) (*github.CombinedStatus, error)
	GetCheckRuns(ctx context.Context, owner, repo, ref string) ([]github.CheckRun, error)
	GetReviews(ctx context.Context, owner, repo string, number int) ([]github.Review, error)
}

// ConfigStore defines the interface for config persistence.
type ConfigStore interface {
	UpdatePR(owner, repo string, number int, sha, state string)
	Save() error
}

// Watcher polls GitHub PRs and triggers notifications on status changes.
type Watcher struct {
	client   GitHubClient // GitHub; nil without a token
	config   *config.Config
	notifier notify.Notifier
	// clients holds the clients of other hosts (Bitbucket), set with SetClient.
	clients map[string]GitHubClient

	// pausedUntil is set per host when it reports a rate limit; that host's PRs are
	// skipped until then so the watcher stops spending requests that are bound to fail.
	pausedUntil map[string]time.Time
	// warnedNoClient remembers hosts already reported as having no client.
	warnedNoClient map[string]bool
	// now is the clock; tests replace it.
	now func() time.Time
	// lastAutoWatch is when the auto-watch searches last ran.
	lastAutoWatch time.Time
	// finders are the auto-watch finders of hosts other than GitHub (AddFinder).
	finders []Finder
}

const noPRsMessage = "No PRs being watched. Add some with 'prw watch <PR_URL>', or 'prw watch --mine' to follow your open PRs."

// defaultRateLimitPause is used when the API doesn't say when the limit resets.
const defaultRateLimitPause = time.Minute

// BitbucketMinInterval is the least time between two checks of a Bitbucket PR,
// whatever the poll interval. Bitbucket Cloud allows 1,000 requests per hour and a
// check costs up to three, so with more PRs each is checked less often: the checks
// get bitbucketCheckBudget requests per hour, leaving the rest to auto-watch and other
// commands.
const BitbucketMinInterval = time.Minute

const (
	bitbucketCheckBudget = 600 // requests per hour
	bitbucketCheckCost   = 3   // PR + merge checks + build statuses
)

// BitbucketInterval is the least time between two checks of each of n Bitbucket PRs.
func BitbucketInterval(n int) time.Duration {
	d := time.Duration(n*bitbucketCheckCost) * time.Hour / bitbucketCheckBudget
	if d < BitbucketMinInterval {
		return BitbucketMinInterval
	}
	return d
}

// New creates a new Watcher.
func New(client GitHubClient, cfg *config.Config, notifier notify.Notifier) *Watcher {
	return &Watcher{
		client:         client,
		config:         cfg,
		notifier:       notifier,
		now:            time.Now,
		clients:        map[string]GitHubClient{},
		pausedUntil:    map[string]time.Time{},
		warnedNoClient: map[string]bool{},
	}
}

// SetClient sets the client used for PRs on host (forge.Bitbucket).
func (w *Watcher) SetClient(host string, client GitHubClient) {
	w.clients[forge.NormalizeHost(host)] = client
}

// clientFor returns the client for PRs on host, or nil if there is none.
func (w *Watcher) clientFor(host string) GitHubClient {
	host = forge.NormalizeHost(host)
	if host == forge.GitHub {
		if w.client == nil || isNilClient(w.client) {
			return nil
		}
		return w.client
	}
	return w.clients[host]
}

// isNilClient catches a typed nil (e.g. a nil *github.Client) stored in the interface.
func isNilClient(c GitHubClient) bool {
	if c == nil {
		return true
	}
	if gc, ok := c.(*github.Client); ok && gc == nil {
		return true
	}
	return false
}

// Run starts the watcher loop and runs until context is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	interval := time.Duration(w.config.PollIntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	fmt.Printf("Starting watcher with %d second poll interval...\n", w.config.PollIntervalSeconds)
	if len(w.config.WatchedPRs) == 0 && len(w.config.AutoWatch) == 0 {
		fmt.Println(noPRsMessage)
		return nil
	}
	for _, source := range w.config.AutoWatch {
		fmt.Printf("Auto-watching %s (refreshed every %s).\n", AutoWatchDescription[source], AutoWatchInterval)
	}

	// Check immediately on startup
	if len(w.config.AutoWatch) > 0 {
		w.syncAutoWatch(ctx)
		w.reloadWatchedPRs()
	}
	w.checkAllPRs(ctx)

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nWatcher stopped.")
			return ctx.Err()
		case <-ticker.C:
			w.reloadWatchedPRs()
			if len(w.config.AutoWatch) > 0 && w.now().Sub(w.lastAutoWatch) >= AutoWatchInterval {
				w.syncAutoWatch(ctx)
				w.reloadWatchedPRs()
			}
			w.checkAllPRs(ctx)
		}
	}
}

// reloadWatchedPRs re-reads the watch list from disk so PRs added with `prw watch` or
// removed with `prw unwatch` while the watcher is running take effect on the next poll.
func (w *Watcher) reloadWatchedPRs() {
	fresh, err := config.Load()
	if err != nil {
		fmt.Printf("Warning: failed to reload config: %v\n", err)
		return
	}
	if len(fresh.WatchedPRs) != len(w.config.WatchedPRs) {
		fmt.Printf("Watch list changed: now watching %d PR(s).\n", len(fresh.WatchedPRs))
	}
	w.config.WatchedPRs = fresh.WatchedPRs
	// `prw watch --mine` / `prw unwatch --mine` may have changed the searches too.
	w.config.AutoWatch = fresh.AutoWatch
}

// RunOnce checks all watched PRs a single time and returns.
func (w *Watcher) RunOnce(ctx context.Context) error {
	fmt.Printf("Running one-time check with %d second poll interval...\n", w.config.PollIntervalSeconds)
	if len(w.config.AutoWatch) > 0 {
		w.syncAutoWatch(ctx)
		w.reloadWatchedPRs()
	}
	if len(w.config.WatchedPRs) == 0 {
		fmt.Println(noPRsMessage)
		return nil
	}
	w.checkAllPRs(ctx)
	return nil
}

func (w *Watcher) checkAllPRs(ctx context.Context) {
	var stillWatched, done []config.WatchedPR

	for i := range w.config.WatchedPRs {
		pr := w.config.WatchedPRs[i]
		if !w.due(pr) {
			stillWatched = append(stillWatched, pr) // paused or checked too recently
			continue
		}
		closed, err := w.checkPR(ctx, &pr)
		if err != nil && ctx.Err() != nil {
			// Stopped mid-round (Ctrl-C): keep this and the remaining PRs unchanged.
			stillWatched = append(stillWatched, w.config.WatchedPRs[i:]...)
			break
		}
		if err != nil {
			stillWatched = append(stillWatched, pr) // keep watching; preserve any partial update (e.g. title)
			if errors.Is(err, errNoClient) {
				continue // reported once by checkPR
			}
			if limited, resetAt := github.IsRateLimited(err); limited {
				// The host's other PRs would fail too: skip them until the limit resets.
				w.pauseForRateLimit(pr.Host, resetAt)
				continue
			}
			fmt.Printf("Error checking PR %s/%s#%d: %v\n", pr.Owner, pr.Repo, pr.Number, err)
			continue
		}
		if closed {
			done = append(done, pr)
			continue
		}
		stillWatched = append(stillWatched, pr)
	}
	w.config.WatchedPRs = stillWatched

	// Persist only the polling state. Saving the whole in-memory config would erase
	// PRs added (or resurrect PRs removed) by other commands since the last reload,
	// and would write run-only overrides such as --on into the config file.
	if err := config.SavePRStates(stillWatched); err != nil {
		fmt.Printf("Warning: failed to save config: %v\n", err)
	}
	if len(done) > 0 {
		if err := config.RemovePRs(done); err != nil {
			fmt.Printf("Warning: failed to remove merged/closed PRs from config: %v\n", err)
		}
	}
}

// due reports whether pr should be checked this round: not while its host is rate
// limited, and Bitbucket PRs at most every BitbucketInterval.
func (w *Watcher) due(pr config.WatchedPR) bool {
	host := forge.NormalizeHost(pr.Host)
	if w.now().Before(w.pausedUntil[host]) {
		return false
	}
	if host == forge.Bitbucket && !pr.LastChecked.IsZero() && w.now().Sub(pr.LastChecked) < BitbucketInterval(w.bitbucketPRs()) {
		return false
	}
	return true
}

func (w *Watcher) bitbucketPRs() int {
	n := 0
	for _, pr := range w.config.WatchedPRs {
		if forge.IsBitbucket(pr.Host) {
			n++
		}
	}
	return n
}

func (w *Watcher) pauseForRateLimit(host string, resetAt time.Time) {
	host = forge.NormalizeHost(host)
	now := w.now()
	if resetAt.Before(now) {
		resetAt = now.Add(defaultRateLimitPause)
	}
	w.pausedUntil[host] = resetAt
	fmt.Printf("⚠️  %s API rate limit reached; pausing checks of its PRs until %s (%s).\n",
		serviceName(host), resetAt.Local().Format("15:04:05"), resetAt.Sub(now).Round(time.Second))
}

func serviceName(host string) string {
	if forge.IsBitbucket(host) {
		return "Bitbucket"
	}
	return "GitHub"
}

// errNoClient is returned for PRs on a host prw has no client for (no token).
var errNoClient = errors.New("no client for this host")

// checkPR checks one PR's CI status and reports whether it is now closed (merged or
// not), in which case the caller should stop watching it.
func (w *Watcher) checkPR(ctx context.Context, pr *config.WatchedPR) (closed bool, err error) {
	client := w.clientFor(pr.Host)
	if client == nil {
		if host := forge.NormalizeHost(pr.Host); !w.warnedNoClient[host] {
			w.warnedNoClient[host] = true
			fmt.Printf("Warning: skipping %s PRs: no %s token configured.\n", serviceName(host), serviceName(host))
		}
		return false, errNoClient
	}
	// Fetch the PR to get its state and current head SHA
	ghPR, err := client.GetPullRequest(ctx, pr.Owner, pr.Repo, pr.Number)
	if err != nil {
		return false, fmt.Errorf("failed to fetch PR: %w", err)
	}

	// Refresh title when available
	if ghPR.Title != "" && ghPR.Title != pr.Title {
		pr.Title = ghPR.Title
	}

	if ghPR.IsClosed() {
		finalState := "closed"
		if ghPR.Merged {
			finalState = "merged"
		}
		// Always notify once, regardless of --on/notification_filter: a merge or close
		// is a lifecycle event, not a CI result, and this is the user's only chance to
		// see it before prw stops watching the PR.
		event := &notify.StatusChangeEvent{
			Host:          pr.Host,
			Owner:         pr.Owner,
			Repo:          pr.Repo,
			Number:        pr.Number,
			Title:         pr.Title,
			PreviousState: github.NormalizeState(pr.LastKnownState),
			CurrentState:  finalState,
			SHA:           ghPR.Head.SHA,
			Timestamp:     time.Now(),
		}
		if err := w.notifier.Notify(event); err != nil {
			fmt.Printf("Warning: notification failed: %v\n", err)
		}
		return true, nil
	}

	currentSHA := ghPR.Head.SHA

	// Aggregate commit statuses and check runs (GitHub Actions) for the head commit
	ci, err := github.FetchCIStatus(ctx, client, pr.Owner, pr.Repo, currentSHA)
	if err != nil {
		return false, fmt.Errorf("failed to fetch status: %w", err)
	}
	github.ApplyNoCI(ci, ghPR.UpdatedAt, w.now())

	currentState := ci.State
	previousState := github.NormalizeState(pr.LastKnownState)
	if previousState == github.StateNoCI && currentState == "pending" && ci.Total == 0 {
		// Comments and label changes also bump updated_at, which restarts the grace
		// period; until an actual check or status shows up, the PR still has no CI.
		currentState = github.StateNoCI
	}

	// Check if status changed. Finding out that a PR has no CI is not a CI result, so
	// it isn't announced (it would otherwise ping once for every such PR).
	notifiedCI := false
	if previousState != "" && previousState != currentState && currentState != github.StateNoCI &&
		shouldNotify(w.config.NotificationFilter, currentState) {
		notifiedCI = true
		event := &notify.StatusChangeEvent{
			Host:          pr.Host,
			Owner:         pr.Owner,
			Repo:          pr.Repo,
			Number:        pr.Number,
			Title:         pr.Title,
			PreviousState: previousState,
			CurrentState:  currentState,
			SHA:           currentSHA,
			FailedChecks:  ci.FailedChecks,
			Timestamp:     time.Now(),
		}

		if err := w.notifier.Notify(event); err != nil {
			fmt.Printf("Warning: notification failed: %v\n", err)
		}
	}

	// Update stored state
	pr.LastKnownSHA = currentSHA
	pr.LastKnownState = currentState
	pr.LastChecked = time.Now()

	if !w.config.DisablePREvents {
		if err := w.checkPREvents(ctx, pr, ghPR, notifiedCI); err != nil {
			return false, err
		}
	}
	return false, nil
}

// checkPREvents notifies about changes of the PR's review decision and merge state.
// notifiedCI tells whether a CI notification was just sent for this PR. Nothing is
// announced the first time a PR is seen (the previous state is empty).
func (w *Watcher) checkPREvents(ctx context.Context, pr *config.WatchedPR, ghPR *github.PullRequest, notifiedCI bool) error {
	reviews, err := w.clientFor(pr.Host).GetReviews(ctx, pr.Owner, pr.Repo, pr.Number)
	if err != nil {
		return fmt.Errorf("failed to fetch reviews: %w", err)
	}
	notified := notifiedCI

	review := github.ReviewDecision(reviews)
	if prev := pr.LastReviewState; prev != "" && prev != review && review != github.ReviewNone &&
		shouldNotifyPREvent(w.config.NotificationFilter, review) {
		w.notifyPREvent(pr, ghPR, notify.EventKindReview, prev, review)
		notified = true
	}
	pr.LastReviewState = review

	merge := github.MergeStatus(ghPR)
	if merge == "" {
		return nil // GitHub is still computing it; keep the last known state
	}
	if prev := pr.LastMergeState; prev != "" && prev != merge && mergeWorthNotifying(prev, merge, notified) &&
		shouldNotifyPREvent(w.config.NotificationFilter, merge) {
		w.notifyPREvent(pr, ghPR, notify.EventKindMerge, prev, merge)
	}
	pr.LastMergeState = merge
	return nil
}

// mergeWorthNotifying decides which merge state changes are announced: new conflicts,
// and a PR becoming ready to merge after something other than its CI was holding it
// back (branch protection, conflicts, an outdated branch). Without branch protection a
// PR goes from unstable to ready as soon as CI passes, which the CI notification
// already says; the same goes for an approval that unblocks it, so "ready" isn't sent
// together with another notification.
func mergeWorthNotifying(prev, merge string, notified bool) bool {
	switch merge {
	case github.MergeConflict:
		return true
	case github.MergeReady:
		return !notified && (prev == github.MergeBlocked || prev == github.MergeConflict || prev == github.MergeBehind)
	}
	return false
}

func (w *Watcher) notifyPREvent(pr *config.WatchedPR, ghPR *github.PullRequest, kind, prev, current string) {
	event := &notify.StatusChangeEvent{
		Kind:          kind,
		Host:          pr.Host,
		Owner:         pr.Owner,
		Repo:          pr.Repo,
		Number:        pr.Number,
		Title:         pr.Title,
		PreviousState: prev,
		CurrentState:  current,
		SHA:           ghPR.Head.SHA,
		Timestamp:     time.Now(),
	}
	if err := w.notifier.Notify(event); err != nil {
		fmt.Printf("Warning: notification failed: %v\n", err)
	}
}

// shouldNotifyPREvent applies the notification filter to review and merge events:
// "fail" keeps the ones that need action (changes requested, conflicts), "success"
// the good news (approved, ready to merge).
func shouldNotifyPREvent(filter, state string) bool {
	switch config.NormalizeNotificationFilter(filter) {
	case config.NotificationFilterFail:
		return state == github.ReviewChangesRequested || state == github.MergeConflict
	case config.NotificationFilterSuccess:
		return state == github.ReviewApproved || state == github.MergeReady
	default:
		return true
	}
}

func shouldNotify(filter, currentState string) bool {
	if !config.IsValidNotificationFilter(filter) {
		filter = config.NotificationFilterChange
	}
	switch filter {
	case config.NotificationFilterFail:
		return currentState == "failure" || currentState == "error"
	case config.NotificationFilterSuccess:
		return currentState == "success"
	default:
		return true
	}
}
