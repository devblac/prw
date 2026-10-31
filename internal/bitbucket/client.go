// Package bitbucket is prw's Bitbucket Cloud client. It answers in the types of the
// github package (pull requests, check runs, reviews), so the watcher, `prw wait`,
// `prw checks` and the MCP server work on Bitbucket pull requests unchanged:
//
//   - a pull request's source commit is its head; OPEN is open, MERGED is merged,
//     DECLINED and SUPERSEDED are closed;
//   - build statuses (Bitbucket Pipelines, Jenkins, ...) become check runs, with
//     their link and duration;
//   - participants who approved or requested changes become reviews;
//   - the merge checks become the merge state (conflict, blocked, ready).
package bitbucket

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/devblac/prw/internal/github"
)

// DefaultBaseURL is the Bitbucket Cloud REST API.
const DefaultBaseURL = "https://api.bitbucket.org/2.0"

const (
	statusesPageLen = 100
	maxStatusPages  = 5
	// mergeabilityTTL is how long a PR's merge checks are reused while the PR itself
	// is unchanged. Bitbucket allows 1,000 requests per hour, and the base branch can
	// move (creating conflicts) without the PR being updated, so they're re-read
	// periodically rather than on every poll.
	mergeabilityTTL = 2 * time.Minute
	// participantsTTL bounds how old the PR read by GetPullRequest may be for
	// GetReviews to reuse its participants instead of fetching the PR again.
	participantsTTL = time.Minute
)

// ErrUnsupported is returned for features prw only has for GitHub so far.
var ErrUnsupported = errors.New("not supported for Bitbucket yet")

// Client talks to the Bitbucket Cloud API. It is safe for concurrent use.
type Client struct {
	http *github.Client
	now  func() time.Time

	mu           sync.Mutex
	prs          map[string]cachedPR
	mergeability map[string]cachedMergeability
}

type cachedPR struct {
	pr        *pullRequest
	fetchedAt time.Time
}

type cachedMergeability struct {
	state     string
	updatedOn time.Time // the PR's updated_on when the checks were read
	fetchedAt time.Time
}

// NewClient returns a client authenticated with an Atlassian API token and the
// account's email (Basic auth), or, without an email, with a workspace / project /
// repository access token (Bearer). With no token it is anonymous, which only works
// for public repositories.
func NewClient(email, token string) *Client {
	h := github.NewClient("")
	h.BaseURL = DefaultBaseURL
	h.Accept = "application/json"
	h.Service = "Bitbucket"
	switch {
	case token != "" && email != "":
		h.AuthHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+token))
	case token != "":
		h.AuthHeader = "Bearer " + token
	}
	return &Client{http: h, now: time.Now, prs: map[string]cachedPR{}, mergeability: map[string]cachedMergeability{}}
}

// SetBaseURL points the client at another server (tests).
func (c *Client) SetBaseURL(base string) { c.http.BaseURL = strings.TrimRight(base, "/") }

// RequestsInLastHour is how many requests the client sent in the last hour.
func (c *Client) RequestsInLastHour() int { return c.http.RequestsInLastHour() }

// RateLimit returns the quota reported by the most recent response, if any.
func (c *Client) RateLimit() github.RateLimit { return c.http.RateLimit() }

type pullRequest struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	State     string    `json:"state"` // OPEN, MERGED, DECLINED, SUPERSEDED
	UpdatedOn time.Time `json:"updated_on"`
	Source    struct {
		Commit struct {
			Hash string `json:"hash"`
		} `json:"commit"`
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	} `json:"source"`
	Destination struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	} `json:"destination"`
	Participants []struct {
		User struct {
			UUID        string `json:"uuid"`
			AccountID   string `json:"account_id"`
			DisplayName string `json:"display_name"`
			Nickname    string `json:"nickname"`
		} `json:"user"`
		Role     string `json:"role"`
		Approved bool   `json:"approved"`
		State    string `json:"state"` // approved, changes_requested, or empty
	} `json:"participants"`
}

func repoPath(workspace, repo string) string {
	return "/repositories/" + url.PathEscape(workspace) + "/" + url.PathEscape(repo)
}

func prKey(workspace, repo string, number int) string {
	return strings.ToLower(workspace) + "/" + strings.ToLower(repo) + "#" + fmt.Sprint(number)
}

func (c *Client) fetchPR(ctx context.Context, workspace, repo string, number int) (*pullRequest, error) {
	var pr pullRequest
	if err := c.http.GetJSON(ctx, fmt.Sprintf("%s/pullrequests/%d", repoPath(workspace, repo), number), &pr); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.prs[prKey(workspace, repo, number)] = cachedPR{pr: &pr, fetchedAt: c.now()}
	c.mu.Unlock()
	return &pr, nil
}

// GetPullRequest fetches a pull request, with its merge state.
func (c *Client) GetPullRequest(ctx context.Context, workspace, repo string, number int) (*github.PullRequest, error) {
	bb, err := c.fetchPR(ctx, workspace, repo, number)
	if err != nil {
		return nil, err
	}
	pr := &github.PullRequest{Number: bb.ID, Title: bb.Title, UpdatedAt: bb.UpdatedOn}
	pr.Head.SHA = bb.Source.Commit.Hash
	pr.Head.Ref = bb.Source.Branch.Name
	pr.Base.Ref = bb.Destination.Branch.Name
	switch strings.ToUpper(bb.State) {
	case "OPEN":
		pr.State = "open"
		pr.MergeableState = c.mergeState(ctx, workspace, repo, number, bb.UpdatedOn)
	case "MERGED":
		pr.State, pr.Merged = "closed", true
	default: // DECLINED, SUPERSEDED
		pr.State = "closed"
	}
	return pr, nil
}

type mergeCheck struct {
	Type     string `json:"type"`
	Status   string `json:"status"` // PASSED, FAILED, ...
	Blocking bool   `json:"blocking"`
	Reason   string `json:"reason"`
}

// mergeState maps the PR's merge checks to a GitHub mergeable_state (see
// github.MergeStatus): "dirty" when git reports conflicts, "blocked" while a blocking
// merge check (approvals, successful builds, open tasks, ...) fails, "clean" otherwise.
// Whether the caller may merge (current_user_permission_check) is ignored: it's about
// who asks, not about the PR. Errors leave it unknown ("").
func (c *Client) mergeState(ctx context.Context, workspace, repo string, number int, updatedOn time.Time) string {
	key := prKey(workspace, repo, number)
	c.mu.Lock()
	cached, ok := c.mergeability[key]
	c.mu.Unlock()
	if ok && cached.updatedOn.Equal(updatedOn) && c.now().Sub(cached.fetchedAt) < mergeabilityTTL {
		return cached.state
	}

	var resp struct {
		Values []mergeCheck `json:"values"`
	}
	path := fmt.Sprintf("%s/pullrequests/%d/mergeability/checks", repoPath(workspace, repo), number)
	if err := c.http.GetJSON(ctx, path, &resp); err != nil {
		return ""
	}
	state := "clean"
	for _, check := range resp.Values {
		failed := strings.EqualFold(check.Status, "FAILED")
		switch {
		case check.Type == "git_mergeability_check" && failed:
			state = "dirty"
		case check.Type == "current_user_permission_check":
		case failed && check.Blocking && state != "dirty":
			state = "blocked"
		}
	}
	c.mu.Lock()
	c.mergeability[key] = cachedMergeability{state: state, updatedOn: updatedOn, fetchedAt: c.now()}
	c.mu.Unlock()
	return state
}

// GetCombinedStatus returns no legacy statuses: Bitbucket's build statuses are all
// reported as check runs by GetCheckRuns. It makes no request.
func (c *Client) GetCombinedStatus(ctx context.Context, workspace, repo, ref string) (*github.CombinedStatus, error) {
	return &github.CombinedStatus{State: "pending", SHA: ref}, nil
}

type buildStatus struct {
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	State     string    `json:"state"` // SUCCESSFUL, FAILED, INPROGRESS, STOPPED
	URL       string    `json:"url"`
	CreatedOn time.Time `json:"created_on"`
	UpdatedOn time.Time `json:"updated_on"`
}

// GetCheckRuns returns the commit's build statuses (Bitbucket Pipelines, Jenkins, ...)
// as check runs. A status is updated in place when its build reruns, so each key
// appears once; IDs grow with the last update so the newest wins on duplicate names.
func (c *Client) GetCheckRuns(ctx context.Context, workspace, repo, ref string) ([]github.CheckRun, error) {
	var runs []github.CheckRun
	path := fmt.Sprintf("%s/commit/%s/statuses?pagelen=%d", repoPath(workspace, repo), url.PathEscape(ref), statusesPageLen)
	for page := 0; page < maxStatusPages && path != ""; page++ {
		var resp struct {
			Values []buildStatus `json:"values"`
			Next   string        `json:"next"`
		}
		if err := c.http.GetJSON(ctx, path, &resp); err != nil {
			return nil, err
		}
		for _, s := range resp.Values {
			runs = append(runs, toCheckRun(s))
		}
		path = c.relative(resp.Next)
	}
	return runs, nil
}

func toCheckRun(s buildStatus) github.CheckRun {
	name := s.Name
	if name == "" {
		name = s.Key
	}
	run := github.CheckRun{ID: s.UpdatedOn.UnixNano(), Name: name, HTMLURL: s.URL, StartedAt: s.CreatedOn}
	switch strings.ToUpper(s.State) {
	case "INPROGRESS":
		run.Status = "in_progress"
	case "SUCCESSFUL":
		run.Status, run.Conclusion, run.CompletedAt = "completed", "success", s.UpdatedOn
	case "STOPPED":
		run.Status, run.Conclusion, run.CompletedAt = "completed", "cancelled", s.UpdatedOn
	default: // FAILED, or anything new: not a pass
		run.Status, run.Conclusion, run.CompletedAt = "completed", "failure", s.UpdatedOn
	}
	return run
}

// relative turns a "next" page URL into a path under the base URL ("" when done).
func (c *Client) relative(next string) string {
	if next == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(next, c.http.BaseURL); ok {
		return rest
	}
	return "" // a link to another host: don't follow it with our credentials
}

// GetReviews returns the current verdict of each participant who approved or
// requested changes, as reviews. It reuses the PR just read by GetPullRequest.
func (c *Client) GetReviews(ctx context.Context, workspace, repo string, number int) ([]github.Review, error) {
	c.mu.Lock()
	cached, ok := c.prs[prKey(workspace, repo, number)]
	c.mu.Unlock()
	pr := cached.pr
	if !ok || c.now().Sub(cached.fetchedAt) > participantsTTL {
		var err error
		if pr, err = c.fetchPR(ctx, workspace, repo, number); err != nil {
			return nil, err
		}
	}
	var reviews []github.Review
	for _, p := range pr.Participants {
		var r github.Review
		r.User.Login = p.User.UUID
		if r.User.Login == "" {
			r.User.Login = p.User.DisplayName
		}
		switch {
		case strings.EqualFold(p.State, "changes_requested"):
			r.State = "CHANGES_REQUESTED"
		case p.Approved || strings.EqualFold(p.State, "approved"):
			r.State = "APPROVED"
		default:
			continue
		}
		reviews = append(reviews, r)
	}
	return reviews, nil
}

// GetJobLog is not available for Bitbucket yet.
func (c *Client) GetJobLog(ctx context.Context, workspace, repo string, jobID int64) (string, error) {
	return "", ErrUnsupported
}

// ListWorkflowRuns is GitHub Actions only (prw flaky).
func (c *Client) ListWorkflowRuns(ctx context.Context, workspace, repo string, max int) ([]github.WorkflowRun, error) {
	return nil, ErrUnsupported
}

// GetAttemptJobs is GitHub Actions only (prw flaky).
func (c *Client) GetAttemptJobs(ctx context.Context, workspace, repo string, runID int64, attempt int) ([]github.Job, error) {
	return nil, ErrUnsupported
}
