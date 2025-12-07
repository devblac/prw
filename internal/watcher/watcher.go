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
}

// New creates a new Watcher.
func New(client GitHubClient, cfg *config.Config, notifier notify.Notifier) *Watcher {
	return &Watcher{
		client:   client,
		config:   cfg,
		notifier: notifier,
	}
}

// Run starts the watcher loop and runs until context is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	interval := time.Duration(w.config.PollIntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	fmt.Printf("Starting watcher with %d second poll interval...\n", w.config.PollIntervalSeconds)
	if len(w.config.WatchedPRs) == 0 {
		fmt.Println("No PRs being watched. Add some with 'prw watch <PR_URL>'.")
		return nil
	}

	// Check immediately on startup
	w.checkAllPRs()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nWatcher stopped.")
			return ctx.Err()
		case <-ticker.C:
			w.checkAllPRs()
		}
	}
}

func (w *Watcher) checkAllPRs() {
	for i := range w.config.WatchedPRs {
		pr := &w.config.WatchedPRs[i]
		if err := w.checkPR(pr); err != nil {
			fmt.Printf("Error checking PR %s/%s#%d: %v\n", pr.Owner, pr.Repo, pr.Number, err)
		}
	}

	// Save config after checking all PRs
	if err := w.config.Save(); err != nil {
		fmt.Printf("Warning: failed to save config: %v\n", err)
	}
}

func (w *Watcher) checkPR(pr *config.WatchedPR) error {
	// Fetch the PR to get current head SHA
	ghPR, err := w.client.GetPullRequest(pr.Owner, pr.Repo, pr.Number)
	if err != nil {
		return fmt.Errorf("failed to fetch PR: %w", err)
	}

	currentSHA := ghPR.Head.SHA

	// Fetch combined status for the head commit
	status, err := w.client.GetCombinedStatus(pr.Owner, pr.Repo, currentSHA)
	if err != nil {
		return fmt.Errorf("failed to fetch status: %w", err)
	}

	currentState := github.NormalizeState(status.State)
	previousState := github.NormalizeState(pr.LastKnownState)

	// Refresh title when available
	if ghPR.Title != "" && ghPR.Title != pr.Title {
		pr.Title = ghPR.Title
	}

	// Check if status changed
	if previousState != "" && previousState != currentState && shouldNotify(w.config.NotificationFilter, currentState) {
		event := &notify.StatusChangeEvent{
			Owner:         pr.Owner,
			Repo:          pr.Repo,
			Number:        pr.Number,
			Title:         pr.Title,
			PreviousState: previousState,
			CurrentState:  currentState,
			SHA:           currentSHA,
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

	return nil
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
