package watcher

import (
	"context"
	"testing"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
	"github.com/devblac/prw/internal/notify"
)

func review(login, state string) github.Review {
	var r github.Review
	r.User.Login = login
	r.State = state
	return r
}

// prEventsCase is one check of PR #1 whose CI stays green, starting from the given
// stored review and merge states.
type prEventsCase struct {
	lastReview, lastMerge string
	reviews               []github.Review
	mergeableState        string
	filter                string
	disabled              bool
}

func runPREventsCheck(t *testing.T, c prEventsCase) (*config.WatchedPR, []*notify.StatusChangeEvent) {
	t.Helper()
	pr := newPR(1, "sha1")
	pr.State = "open"
	pr.MergeableState = c.mergeableState
	client := &mockGitHubClient{
		prs:      map[string]*github.PullRequest{"owner/repo/1": pr},
		statuses: map[string]*github.CombinedStatus{"sha1": {State: "success", TotalCount: 1}},
		reviews:  map[string][]github.Review{"owner/repo/1": c.reviews},
	}
	watchedPR := watched(1, "success")
	watchedPR.LastReviewState, watchedPR.LastMergeState = c.lastReview, c.lastMerge
	cfg := &config.Config{NotificationFilter: c.filter, DisablePREvents: c.disabled, WatchedPRs: []config.WatchedPR{watchedPR}}
	notifier := &mockNotifier{}

	if _, err := New(client, cfg, notifier).checkPR(context.Background(), &cfg.WatchedPRs[0]); err != nil {
		t.Fatal(err)
	}
	return &cfg.WatchedPRs[0], notifier.events
}

func TestCheckPR_NotifiesApproval(t *testing.T) {
	pr, events := runPREventsCheck(t, prEventsCase{
		lastReview: github.ReviewNone, lastMerge: github.MergeBlocked,
		reviews:        []github.Review{review("alice", "COMMENTED"), review("alice", "APPROVED")},
		mergeableState: "blocked", // e.g. a second approval is required
	})
	if len(events) != 1 || events[0].Kind != notify.EventKindReview || events[0].PreviousState != "none" || events[0].CurrentState != "approved" {
		t.Fatalf("events = %+v, want one none → approved review event", events)
	}
	if pr.LastReviewState != github.ReviewApproved || pr.LastMergeState != github.MergeBlocked {
		t.Errorf("stored review=%q merge=%q", pr.LastReviewState, pr.LastMergeState)
	}
}

func TestCheckPR_NotifiesChangesRequested(t *testing.T) {
	_, events := runPREventsCheck(t, prEventsCase{
		lastReview: github.ReviewApproved, lastMerge: github.MergeReady,
		reviews:        []github.Review{review("alice", "APPROVED"), review("bob", "CHANGES_REQUESTED")},
		mergeableState: "clean",
	})
	if len(events) != 1 || events[0].CurrentState != github.ReviewChangesRequested {
		t.Fatalf("events = %+v, want approved → changes_requested", events)
	}
}

func TestCheckPR_FirstObservationDoesNotNotify(t *testing.T) {
	// Existing watch lists (and newly added PRs) have no stored review/merge state yet.
	pr, events := runPREventsCheck(t, prEventsCase{
		reviews:        []github.Review{review("alice", "APPROVED")},
		mergeableState: "dirty",
	})
	if len(events) != 0 {
		t.Errorf("events = %+v, want none on the first check", events)
	}
	if pr.LastReviewState != github.ReviewApproved || pr.LastMergeState != github.MergeConflict {
		t.Errorf("stored review=%q merge=%q, want them recorded", pr.LastReviewState, pr.LastMergeState)
	}
}

func TestCheckPR_NotifiesNewConflict(t *testing.T) {
	_, events := runPREventsCheck(t, prEventsCase{lastReview: github.ReviewNone, lastMerge: github.MergeReady, mergeableState: "dirty"})
	if len(events) != 1 || events[0].Kind != notify.EventKindMerge || events[0].CurrentState != github.MergeConflict {
		t.Fatalf("events = %+v, want ready → conflict", events)
	}
}

func TestCheckPR_ReadyToMergeAfterBranchProtectionIsSatisfied(t *testing.T) {
	// The required approval came from someone whose review was already counted,
	// or a required reviewer was satisfied by CODEOWNERS: only the merge state changes.
	_, events := runPREventsCheck(t, prEventsCase{
		lastReview: github.ReviewApproved, lastMerge: github.MergeBlocked,
		reviews:        []github.Review{review("alice", "APPROVED")},
		mergeableState: "clean",
	})
	if len(events) != 1 || events[0].CurrentState != github.MergeReady || events[0].PreviousState != github.MergeBlocked {
		t.Fatalf("events = %+v, want blocked → ready", events)
	}
}

func TestCheckPR_ReadyIsNotSentWithTheApprovalThatCausedIt(t *testing.T) {
	_, events := runPREventsCheck(t, prEventsCase{
		lastReview: github.ReviewNone, lastMerge: github.MergeBlocked,
		reviews:        []github.Review{review("alice", "APPROVED")},
		mergeableState: "clean",
	})
	if len(events) != 1 || events[0].Kind != notify.EventKindReview {
		t.Fatalf("events = %+v, want only the approval", events)
	}
}

func TestCheckPR_UnstableToReadyIsLeftToTheCINotification(t *testing.T) {
	// No branch protection: the PR is "unstable" while checks run and "clean" once they
	// pass, which the CI success notification already announces.
	_, events := runPREventsCheck(t, prEventsCase{lastReview: github.ReviewNone, lastMerge: github.MergeUnstable, mergeableState: "clean"})
	if len(events) != 0 {
		t.Errorf("events = %+v, want none", events)
	}
}

func TestCheckPR_UnknownMergeStateKeepsTheLastOne(t *testing.T) {
	pr, events := runPREventsCheck(t, prEventsCase{lastReview: github.ReviewNone, lastMerge: github.MergeConflict, mergeableState: "unknown"})
	if len(events) != 0 || pr.LastMergeState != github.MergeConflict {
		t.Errorf("events=%+v merge=%q; while GitHub computes mergeability nothing changes", events, pr.LastMergeState)
	}
}

func TestCheckPR_DismissedReviewWithdrawsTheVerdict(t *testing.T) {
	pr, events := runPREventsCheck(t, prEventsCase{
		lastReview: github.ReviewChangesRequested, lastMerge: github.MergeBlocked,
		reviews:        []github.Review{review("bob", "CHANGES_REQUESTED"), review("bob", "DISMISSED")},
		mergeableState: "blocked",
	})
	if len(events) != 0 || pr.LastReviewState != github.ReviewNone {
		t.Errorf("events=%+v review=%q, want silently back to none", events, pr.LastReviewState)
	}
}

func TestCheckPR_PREventFilters(t *testing.T) {
	tests := []struct {
		filter string
		c      prEventsCase
		want   int
	}{
		{config.NotificationFilterFail, prEventsCase{lastReview: "none", lastMerge: "blocked", reviews: []github.Review{review("a", "APPROVED")}, mergeableState: "blocked"}, 0},
		{config.NotificationFilterFail, prEventsCase{lastReview: "none", lastMerge: "ready", mergeableState: "dirty"}, 1},
		{config.NotificationFilterSuccess, prEventsCase{lastReview: "none", lastMerge: "ready", mergeableState: "dirty"}, 0},
		{config.NotificationFilterSuccess, prEventsCase{lastReview: "approved", lastMerge: "blocked", reviews: []github.Review{review("a", "APPROVED")}, mergeableState: "clean"}, 1},
	}
	for _, tt := range tests {
		tt.c.filter = tt.filter
		if _, events := runPREventsCheck(t, tt.c); len(events) != tt.want {
			t.Errorf("filter %s, %+v: %d events, want %d", tt.filter, tt.c, len(events), tt.want)
		}
	}
}

func TestCheckPR_PREventsCanBeDisabled(t *testing.T) {
	pr, events := runPREventsCheck(t, prEventsCase{lastReview: "none", lastMerge: "ready", mergeableState: "dirty", disabled: true})
	if len(events) != 0 || pr.LastMergeState != "ready" {
		t.Errorf("events=%+v merge=%q, want pr_events=false to skip review/merge checks", events, pr.LastMergeState)
	}
}
