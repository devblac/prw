package watcher

import (
	"context"
	"fmt"
	"time"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
	"github.com/devblac/prw/internal/notify"
)

// GitHubClient defines the interface for GitHub operations.
type GitHubClient interface {
	GetPullRequest(owner, repo string, number int) (*github.PullRequest, error)
	GetCombinedStatus(owner, repo, ref string) (*github.CombinedStatus, error)
	GetCheckRuns(owner, repo, ref string) ([]github.CheckRun, error)
}

// ConfigStore defines the interface for config persistence.
type ConfigStore interface {
	UpdatePR(owner, repo string, number int, sha, state string)
	Save() error
}

// Watcher polls GitHub PRs and triggers notifications on status changes.
type Watcher struct {
	client   GitHubClient
	config   *config.Config
	notifier notify.Notifier

	// pausedUntil is set when GitHub reports a rate limit; polls are skipped until then
	// so the watcher stops spending requests that are bound to fail.
	pausedUntil time.Time
	// now is the clock; tests replace it.
	now func() time.Time
	// lastAutoWatch is when the auto-watch searches last ran.
	lastAutoWatch time.Time
}

const noPRsMessage = "No PRs being watched. Add some with 'prw watch <PR_URL>', or 'prw watch --mine' to follow your open PRs."

// defaultRateLimitPause is used when GitHub doesn't say when the limit resets.
const defaultRateLimitPause = time.Minute

// New creates a new Watcher.
func New(client GitHubClient, cfg *config.Config, notifier notify.Notifier) *Watcher {
	return &Watcher{
		client:   client,
		config:   cfg,
		notifier: notifier,
		now:      time.Now,
	}
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
		w.syncAutoWatch()
		w.reloadWatchedPRs()
	}
	w.checkAllPRs()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nWatcher stopped.")
			return ctx.Err()
		case <-ticker.C:
			if w.now().Before(w.pausedUntil) {
				continue // rate limited; the pause message was printed when it started
			}
			w.reloadWatchedPRs()
			if len(w.config.AutoWatch) > 0 && w.now().Sub(w.lastAutoWatch) >= AutoWatchInterval {
				w.syncAutoWatch()
				w.reloadWatchedPRs()
			}
			w.checkAllPRs()
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
		w.syncAutoWatch()
		w.reloadWatchedPRs()
	}
	if len(w.config.WatchedPRs) == 0 {
		fmt.Println(noPRsMessage)
		return nil
	}
	w.checkAllPRs()
	return nil
}

func (w *Watcher) checkAllPRs() {
	var stillWatched, done []config.WatchedPR

	for i := range w.config.WatchedPRs {
		pr := w.config.WatchedPRs[i]
		closed, err := w.checkPR(&pr)
		if err != nil {
			stillWatched = append(stillWatched, pr) // keep watching; preserve any partial update (e.g. title)
			if limited, resetAt := github.IsRateLimited(err); limited {
				// Every remaining request this round would fail too: keep the rest of
				// the list unchanged and pause until the limit resets.
				stillWatched = append(stillWatched, w.config.WatchedPRs[i+1:]...)
				w.pauseForRateLimit(resetAt)
				break
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

func (w *Watcher) pauseForRateLimit(resetAt time.Time) {
	now := w.now()
	if resetAt.Before(now) {
		resetAt = now.Add(defaultRateLimitPause)
	}
	w.pausedUntil = resetAt
	fmt.Printf("⚠️  GitHub API rate limit reached; pausing checks until %s (%s).\n",
		resetAt.Local().Format("15:04:05"), resetAt.Sub(now).Round(time.Second))
}

// checkPR checks one PR's CI status and reports whether it is now closed (merged or
// not), in which case the caller should stop watching it.
func (w *Watcher) checkPR(pr *config.WatchedPR) (closed bool, err error) {
	// Fetch the PR to get its state and current head SHA
	ghPR, err := w.client.GetPullRequest(pr.Owner, pr.Repo, pr.Number)
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
	ci, err := github.FetchCIStatus(w.client, pr.Owner, pr.Repo, currentSHA)
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
	if previousState != "" && previousState != currentState && currentState != github.StateNoCI &&
		shouldNotify(w.config.NotificationFilter, currentState) {
		event := &notify.StatusChangeEvent{
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

	return false, nil
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
