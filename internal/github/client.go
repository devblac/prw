package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	checkRunsPerPage = 100
	maxCheckRunPages = 10
)

// Client is a simple GitHub API client.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewClient creates a new GitHub client with a 15-second timeout.
func NewClient(token string) *Client {
	return &Client{
		BaseURL: "https://api.github.com",
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// APIError is returned when GitHub responds with a non-200 status code.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GitHub API returned %d: %s", e.StatusCode, e.Body)
}

// PullRequest represents a GitHub pull request.
type PullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"` // open or closed
	Merged bool   `json:"merged"`
	Head   struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

// IsClosed reports whether the PR is closed, merged or not (GitHub reports both as
// state "closed"; Merged distinguishes them).
func (pr *PullRequest) IsClosed() bool {
	return NormalizeState(pr.State) == "closed"
}

// CombinedStatus represents the combined legacy commit status for a commit.
// GitHub Actions does not report here; see CheckRun.
type CombinedStatus struct {
	State      string         `json:"state"` // pending, success, failure, error
	SHA        string         `json:"sha"`
	TotalCount int            `json:"total_count"`
	Statuses   []CommitStatus `json:"statuses"`
}

// CommitStatus is a single legacy commit status (e.g. from an external CI).
type CommitStatus struct {
	Context   string `json:"context"`
	State     string `json:"state"`
	TargetURL string `json:"target_url"`
}

// CheckRun represents a single check run (e.g. a GitHub Actions job).
type CheckRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`     // queued, in_progress, completed, ...
	Conclusion string `json:"conclusion"` // success, failure, neutral, cancelled, ...
	HTMLURL    string `json:"html_url"`
}

type checkRunsResponse struct {
	TotalCount int        `json:"total_count"`
	CheckRuns  []CheckRun `json:"check_runs"`
}

// FailedCheck identifies a commit status or check run that did not pass.
type FailedCheck struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// CIStatus is the aggregated CI result across commit statuses and check runs.
type CIStatus struct {
	State        string // pending, success, failure, error
	FailedChecks []FailedCheck
}

// CIFetcher is the subset of GitHub operations needed to compute CI status.
type CIFetcher interface {
	GetCombinedStatus(owner, repo, ref string) (*CombinedStatus, error)
	GetCheckRuns(owner, repo, ref string) ([]CheckRun, error)
}

// ParsePRURL extracts owner, repo, and PR number from a GitHub PR URL.
func ParsePRURL(prURL string) (owner, repo string, number int, err error) {
	// Match patterns like:
	// https://github.com/owner/repo/pull/123
	// github.com/owner/repo/pull/123
	re := regexp.MustCompile(`(?:https?://)?github\.com/([^/]+)/([^/]+)/pull/(\d+)`)
	matches := re.FindStringSubmatch(prURL)
	if len(matches) != 4 {
		return "", "", 0, fmt.Errorf("invalid GitHub PR URL format")
	}

	number, err = strconv.Atoi(matches[3])
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid PR number: %w", err)
	}

	return matches[1], matches[2], number, nil
}

// GetPullRequest fetches a pull request by owner, repo, and PR number.
func (c *Client) GetPullRequest(owner, repo string, number int) (*PullRequest, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)

	var pr PullRequest
	if err := c.getJSON(path, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// GetCombinedStatus fetches the combined legacy commit status for a commit.
func (c *Client) GetCombinedStatus(owner, repo, ref string) (*CombinedStatus, error) {
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/status", owner, repo, url.PathEscape(ref))

	var status CombinedStatus
	if err := c.getJSON(path, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// GetCheckRuns fetches the latest check runs (e.g. GitHub Actions jobs) for a commit.
func (c *Client) GetCheckRuns(owner, repo, ref string) ([]CheckRun, error) {
	var runs []CheckRun
	for page := 1; page <= maxCheckRunPages; page++ {
		path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?per_page=%d&page=%d",
			owner, repo, url.PathEscape(ref), checkRunsPerPage, page)

		var resp checkRunsResponse
		if err := c.getJSON(path, &resp); err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
				return nil, fmt.Errorf("%w (fine-grained tokens need the \"Checks: read\" permission)", err)
			}
			return nil, err
		}

		runs = append(runs, resp.CheckRuns...)
		if len(resp.CheckRuns) < checkRunsPerPage || len(runs) >= resp.TotalCount {
			break
		}
	}
	return runs, nil
}

// FetchCIStatus fetches commit statuses and check runs for a commit and aggregates them.
func FetchCIStatus(c CIFetcher, owner, repo, ref string) (*CIStatus, error) {
	status, err := c.GetCombinedStatus(owner, repo, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch commit status: %w", err)
	}

	runs, err := c.GetCheckRuns(owner, repo, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch check runs: %w", err)
	}

	return SummarizeCI(status, runs), nil
}

// SummarizeCI aggregates legacy commit statuses and check runs into a single state.
// Precedence: failure > error > pending > success. With no CI signal at all, the
// state is pending.
func SummarizeCI(status *CombinedStatus, runs []CheckRun) *CIStatus {
	var failed []FailedCheck
	var hasFailure, hasError, hasPending bool

	if status != nil {
		legacyState := NormalizeState(status.State)
		// The combined status endpoint reports "pending" when no statuses exist at
		// all, which must not mask the result of check runs.
		noStatuses := legacyState == "pending" && status.TotalCount == 0
		if legacyState != "" && !noStatuses {
			switch legacyState {
			case "success":
			case "failure":
				hasFailure = true
			case "error":
				hasError = true
			default:
				hasPending = true
			}
		}
		for _, s := range status.Statuses {
			if st := NormalizeState(s.State); st == "failure" || st == "error" {
				failed = append(failed, FailedCheck{Name: s.Context, URL: s.TargetURL})
			}
		}
	}

	for _, run := range runs {
		if NormalizeState(run.Status) != "completed" {
			hasPending = true
			continue
		}
		switch NormalizeState(run.Conclusion) {
		case "success", "neutral", "skipped":
		default:
			hasFailure = true
			failed = append(failed, FailedCheck{Name: run.Name, URL: run.HTMLURL})
		}
	}

	state := "success"
	switch {
	case hasFailure:
		state = "failure"
	case hasError:
		state = "error"
	case hasPending:
		state = "pending"
	case !hasCISignal(status, runs):
		state = "pending"
	}

	return &CIStatus{State: state, FailedChecks: failed}
}

func hasCISignal(status *CombinedStatus, runs []CheckRun) bool {
	if len(runs) > 0 {
		return true
	}
	if status == nil {
		return false
	}
	legacyState := NormalizeState(status.State)
	return legacyState != "" && !(legacyState == "pending" && status.TotalCount == 0)
}

// getJSON performs an authenticated GET request and decodes the JSON response into v.
func (c *Client) getJSON(path string, v interface{}) error {
	req, err := http.NewRequest("GET", c.BaseURL+path, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	return nil
}

// FormatPRURL constructs a GitHub PR URL.
func FormatPRURL(owner, repo string, number int) string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number)
}

// NormalizeState normalizes status state strings.
func NormalizeState(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}
