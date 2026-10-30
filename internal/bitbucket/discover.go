package bitbucket

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// prListFields asks list endpoints for just what the dashboard shows, participants
// included, so one request covers every PR on a page.
const prListFields = "next,values.id,values.title,values.state,values.updated_on," +
	"values.source.branch.name,values.source.commit.hash," +
	"values.destination.branch.name,values.destination.repository.full_name," +
	"values.participants.approved,values.participants.state,values.participants.user.uuid"

const (
	listPageLen  = 50
	maxListPages = 10
)

// ListedPR is a pull request as returned by Bitbucket's list endpoints.
type ListedPR struct {
	Workspace string
	Repo      string
	Number    int
	Title     string
	State     string // OPEN, MERGED, DECLINED, SUPERSEDED
	UpdatedOn time.Time
	Source    string // branch
	Target    string // branch
	SHA       string
	// Approvals counts participants who approved; ChangesRequested is set when a
	// participant requested changes.
	Approvals        int
	ChangesRequested bool
}

func toListed(pr pullRequest) ListedPR {
	l := ListedPR{
		Number: pr.ID, Title: pr.Title, State: strings.ToUpper(pr.State), UpdatedOn: pr.UpdatedOn,
		Source: pr.Source.Branch.Name, Target: pr.Destination.Branch.Name, SHA: pr.Source.Commit.Hash,
	}
	if ws, repo, ok := strings.Cut(pr.Destination.Repository.FullName, "/"); ok {
		l.Workspace, l.Repo = ws, repo
	}
	for _, p := range pr.Participants {
		switch {
		case strings.EqualFold(p.State, "changes_requested"):
			l.ChangesRequested = true
		case p.Approved:
			l.Approvals++
		}
	}
	return l
}

// listPRs follows the pages of a pull request list endpoint.
func (c *Client) listPRs(ctx context.Context, path string) ([]ListedPR, error) {
	var out []ListedPR
	for page := 0; page < maxListPages && path != ""; page++ {
		var resp struct {
			Values []pullRequest `json:"values"`
			Next   string        `json:"next"`
		}
		if err := c.http.GetJSON(ctx, path, &resp); err != nil {
			return nil, err
		}
		for _, pr := range resp.Values {
			out = append(out, toListed(pr))
		}
		path = c.relative(resp.Next)
	}
	return out, nil
}

// CurrentUser returns the authenticated user's UUID (e.g. "{a1b2...}").
func (c *Client) CurrentUser(ctx context.Context) (string, error) {
	var u struct {
		UUID string `json:"uuid"`
	}
	if err := c.http.GetJSON(ctx, "/user?fields=uuid", &u); err != nil {
		return "", err
	}
	if u.UUID == "" {
		return "", errors.New("Bitbucket didn't say who the token belongs to")
	}
	return u.UUID, nil
}

// Workspaces returns the slugs of the workspaces the authenticated user belongs to.
func (c *Client) Workspaces(ctx context.Context) ([]string, error) {
	var lastErr error
	// /user/workspaces replaced /user/permissions/workspaces; try both.
	for _, path := range []string{"/user/workspaces?pagelen=100&fields=values.workspace.slug", "/user/permissions/workspaces?pagelen=100&fields=values.workspace.slug"} {
		var resp struct {
			Values []struct {
				Workspace struct {
					Slug string `json:"slug"`
				} `json:"workspace"`
			} `json:"values"`
		}
		if err := c.http.GetJSON(ctx, path, &resp); err != nil {
			lastErr = err
			continue
		}
		var slugs []string
		for _, v := range resp.Values {
			if v.Workspace.Slug != "" {
				slugs = append(slugs, v.Workspace.Slug)
			}
		}
		return slugs, nil
	}
	return nil, lastErr
}

// UserPullRequests lists the pull requests authored by user (a UUID or account id)
// in workspace, across its repositories. state is OPEN or MERGED; a non-zero
// updatedSince keeps only PRs updated since then.
func (c *Client) UserPullRequests(ctx context.Context, workspace, user, state string, updatedSince time.Time) ([]ListedPR, error) {
	q := url.Values{}
	q.Set("state", state)
	q.Set("pagelen", fmt.Sprint(listPageLen))
	q.Set("fields", prListFields)
	if !updatedSince.IsZero() {
		q.Set("q", fmt.Sprintf("updated_on > %s", updatedSince.UTC().Format(time.RFC3339)))
	}
	path := fmt.Sprintf("/workspaces/%s/pullrequests/%s?%s", url.PathEscape(workspace), url.PathEscape(user), q.Encode())
	return c.listPRs(ctx, path)
}

// RecentRepos returns the slugs of the repositories in workspace updated since
// since, most recent first, at most max.
func (c *Client) RecentRepos(ctx context.Context, workspace string, since time.Time, max int) ([]string, error) {
	q := url.Values{}
	q.Set("sort", "-updated_on")
	q.Set("pagelen", "100")
	q.Set("fields", "next,values.slug")
	q.Set("q", fmt.Sprintf("updated_on > %s", since.UTC().Format(time.RFC3339)))
	path := "/repositories/" + url.PathEscape(workspace) + "?" + q.Encode()
	var slugs []string
	for page := 0; page < maxListPages && path != "" && len(slugs) < max; page++ {
		var resp struct {
			Values []struct {
				Slug string `json:"slug"`
			} `json:"values"`
			Next string `json:"next"`
		}
		if err := c.http.GetJSON(ctx, path, &resp); err != nil {
			return nil, err
		}
		for _, v := range resp.Values {
			slugs = append(slugs, v.Slug)
		}
		path = c.relative(resp.Next)
	}
	if len(slugs) > max {
		slugs = slugs[:max]
	}
	return slugs, nil
}

// ReviewPullRequests lists the open pull requests of a repository where user is a
// reviewer. Bitbucket can only filter by reviewer per repository.
func (c *Client) ReviewPullRequests(ctx context.Context, workspace, repo, user string) ([]ListedPR, error) {
	q := url.Values{}
	q.Set("q", fmt.Sprintf(`state = "OPEN" AND reviewers.uuid = %q`, user))
	q.Set("pagelen", fmt.Sprint(listPageLen))
	q.Set("fields", prListFields)
	return c.listPRs(ctx, repoPath(workspace, repo)+"/pullrequests?"+q.Encode())
}

// MergeStateWithin returns the merge state of an open PR like GetPullRequest does,
// reusing a previous answer for up to ttl while the PR's updated_on is unchanged.
// The dashboard uses a long ttl: it shows many PRs within the same request budget.
func (c *Client) MergeStateWithin(ctx context.Context, workspace, repo string, number int, updatedOn time.Time, ttl time.Duration) string {
	key := prKey(workspace, repo, number)
	c.mu.Lock()
	cached, ok := c.mergeability[key]
	c.mu.Unlock()
	if ok && cached.updatedOn.Equal(updatedOn) && c.now().Sub(cached.fetchedAt) < ttl {
		return cached.state
	}
	return c.mergeState(ctx, workspace, repo, number, updatedOn)
}
