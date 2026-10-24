package github

import (
	"context"
	"fmt"
	"strings"
)

// Review decisions, derived from the reviews of a PR.
const (
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewNone             = "none"
)

// Merge states, derived from a PR's mergeable_state.
const (
	MergeReady    = "ready"    // clean: every requirement is met
	MergeConflict = "conflict" // dirty: merge conflicts with the base branch
	MergeBlocked  = "blocked"  // a branch protection requirement isn't met yet
	MergeBehind   = "behind"   // the branch must be updated with the base first
	MergeUnstable = "unstable" // mergeable, but non-required checks didn't pass
	MergeDraft    = "draft"
)

const (
	reviewsPerPage = 100
	// maxReviewsPages only guards against a runaway loop: reviews come oldest first,
	// so stopping early would drop the newest verdicts. Every page is fetched (with a
	// free 304 when unchanged), so 10,000 reviews is far beyond any real PR.
	maxReviewsPages = 100
)

// Review is a single submitted pull request review.
type Review struct {
	ID   int64 `json:"id"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	State string `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING
}

// GetReviews fetches the reviews of a pull request, oldest first.
func (c *Client) GetReviews(ctx context.Context, owner, repo string, number int) ([]Review, error) {
	var reviews []Review
	for page := 1; page <= maxReviewsPages; page++ {
		path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=%d&page=%d", owner, repo, number, reviewsPerPage, page)
		var batch []Review
		if err := c.getJSON(ctx, path, &batch); err != nil {
			return nil, err
		}
		reviews = append(reviews, batch...)
		if len(batch) < reviewsPerPage {
			break
		}
	}
	return reviews, nil
}

// ReviewDecision summarizes reviews like GitHub's review decision: changes requested
// if any reviewer's latest review requests changes, otherwise approved if any
// reviewer's latest review approves, otherwise none. Comments don't change a
// reviewer's verdict; a dismissed review withdraws it. Unlike GitHub's own decision,
// it doesn't know whether the approvals satisfy branch protection (MergeStatus does).
func ReviewDecision(reviews []Review) string {
	verdict := map[string]string{} // reviewer → latest APPROVED / CHANGES_REQUESTED
	for _, r := range reviews {
		login := strings.ToLower(r.User.Login)
		switch strings.ToUpper(r.State) {
		case "APPROVED", "CHANGES_REQUESTED":
			verdict[login] = strings.ToUpper(r.State)
		case "DISMISSED":
			delete(verdict, login)
		}
	}
	decision := ReviewNone
	for _, v := range verdict {
		if v == "CHANGES_REQUESTED" {
			return ReviewChangesRequested
		}
		decision = ReviewApproved
	}
	return decision
}

// MergeStatus maps the PR's mergeable_state to one of the Merge* constants. It
// returns "" while GitHub is still computing it ("unknown"), when the previous value
// should be kept.
func MergeStatus(pr *PullRequest) string {
	switch strings.ToLower(pr.MergeableState) {
	case "clean", "has_hooks":
		return MergeReady
	case "dirty":
		return MergeConflict
	case "blocked":
		return MergeBlocked
	case "behind":
		return MergeBehind
	case "unstable":
		return MergeUnstable
	case "draft":
		return MergeDraft
	}
	return ""
}
