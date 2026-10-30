// Package dashboard builds `prw dashboard`: a live table of the pull requests you
// care about (yours, the ones to review, recently merged ones) across GitHub and
// Bitbucket, grouped by task, with one column per target branch.
//
// Grouping and branch ordering are heuristics with escape hatches, so they work
// for most teams without configuration:
//
//   - A task is the issue key in the PR's source branch or title (e.g. PROJ-123,
//     as used by Jira, Linear, YouTrack...), else the source branch without
//     cherry-pick / backport decorations. issue_pattern and --group-by override it.
//   - Target branches are ranked dev → staging/qa → rc/release → main/master/prod,
//     and branches of the same rank share a column (dev|develop, main|master).
//     branch_order overrides it.
package dashboard

import (
	"time"

	"github.com/devblac/prw/internal/forge"
)

// Roles of a PR in the dashboard.
const (
	RoleMine   = "mine"
	RoleReview = "review"
)

// PR states.
const (
	StateOpen   = "open"
	StateMerged = "merged"
	StateClosed = "closed"
)

// Item is one pull request on the dashboard.
type Item struct {
	Ref       forge.Ref `json:"-"`
	URL       string    `json:"url"`
	Host      string    `json:"host"`
	Repo      string    `json:"repo"` // owner/repo
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Source    string    `json:"source_branch"`
	Target    string    `json:"target_branch"`
	State     string    `json:"state"` // open, merged, closed
	Role      string    `json:"role"`  // mine, review
	UpdatedAt time.Time `json:"updated_at"`
	HeadSHA   string    `json:"sha,omitempty"`

	// CI is the aggregated state (success, failure, error, pending, no_ci), empty
	// until known; CIDone/CITotal count finished checks.
	CI           string   `json:"ci,omitempty"`
	CIDone       int      `json:"ci_done,omitempty"`
	CITotal      int      `json:"ci_total,omitempty"`
	FailedChecks []string `json:"failed_checks,omitempty"`
	// Review is approved, changes_requested or none; Approvals counts approvals.
	Review    string `json:"review,omitempty"`
	Approvals int    `json:"approvals,omitempty"`
	// Merge is ready, conflict, blocked, behind, unstable or draft; empty if unknown.
	Merge string `json:"merge,omitempty"`
}

// Key identifies an item across refreshes.
func (it Item) Key() string {
	return forge.NormalizeHost(it.Ref.Host) + "/" + it.Ref.String()
}
