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
	"sync"
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

	// mu guards cache and rateLimit. cache holds the last ETag and body per request
	// path: a long-running watcher re-asks for the same resources every poll, and a
	// conditional request answered with 304 Not Modified does not count against the
	// rate limit.
	mu        sync.Mutex
	cache     map[string]cachedResponse
	rateLimit RateLimit
}

type cachedResponse struct {
	etag string
	body []byte
}

// maxCacheEntries bounds the ETag cache; paths include commit SHAs, so it would
// otherwise grow with every push to a watched PR.
const maxCacheEntries = 512

// RateLimit is the API quota reported by the most recent response.
type RateLimit struct {
	Limit     int
	Remaining int
	Reset     time.Time
	Known     bool
}

// RateLimit returns the quota reported by the most recent API response.
func (c *Client) RateLimit() RateLimit {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rateLimit
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
	// RateLimited is set when GitHub rejected the request because of a primary or
	// secondary rate limit. ResetAt is when it is worth retrying (zero if unknown).
	RateLimited bool
	ResetAt     time.Time
}

func (e *APIError) Error() string {
	if e.RateLimited {
		if !e.ResetAt.IsZero() {
			return fmt.Sprintf("GitHub API rate limit exceeded (%d); resets at %s", e.StatusCode, e.ResetAt.Local().Format("15:04:05"))
		}
		return fmt.Sprintf("GitHub API rate limit exceeded (%d)", e.StatusCode)
	}
	return fmt.Sprintf("GitHub API returned %d: %s", e.StatusCode, e.Body)
}

// IsRateLimited reports whether err is (or wraps) a rate-limit APIError, and when to
// retry (zero if unknown).
func IsRateLimited(err error) (bool, time.Time) {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RateLimited {
		return true, apiErr.ResetAt
	}
	return false, time.Time{}
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
	// Total and Pending count individual commit statuses and check runs; Pending
	// ones have not finished yet.
	Total   int
	Pending int
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
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden && !apiErr.RateLimited {
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

// SearchResultPR is a pull request returned by SearchPullRequests.
type SearchResultPR struct {
	Owner  string
	Repo   string
	Number int
	Title  string
}

// SearchResult holds the pull requests matching a search query.
type SearchResult struct {
	PRs []SearchResultPR
	// Total is how many items GitHub says match; it can exceed len(PRs) when the
	// results were capped.
	Total int
	// Incomplete is set when GitHub timed out before finding every match, or when the
	// results were capped, so absence from PRs doesn't prove a PR stopped matching.
	Incomplete bool
}

const (
	searchPerPage  = 100
	maxSearchPages = 3
)

type searchIssuesResponse struct {
	TotalCount        int  `json:"total_count"`
	IncompleteResults bool `json:"incomplete_results"`
	Items             []struct {
		Number      int       `json:"number"`
		Title       string    `json:"title"`
		HTMLURL     string    `json:"html_url"`
		PullRequest *struct{} `json:"pull_request"`
	} `json:"items"`
}

// SearchPullRequests runs a GitHub issue search (e.g. "is:pr is:open author:@me") and
// returns the pull requests it finds, up to 300. Qualifiers like @me need a token.
func (c *Client) SearchPullRequests(query string) (*SearchResult, error) {
	result := &SearchResult{}
	for page := 1; page <= maxSearchPages; page++ {
		path := fmt.Sprintf("/search/issues?q=%s&per_page=%d&page=%d", url.QueryEscape(query), searchPerPage, page)
		var resp searchIssuesResponse
		if err := c.getJSON(path, &resp); err != nil {
			return nil, err
		}
		result.Total = resp.TotalCount
		if resp.IncompleteResults {
			result.Incomplete = true
		}
		for _, item := range resp.Items {
			if item.PullRequest == nil {
				continue // an issue, not a PR
			}
			owner, repo, number, err := ParsePRURL(item.HTMLURL)
			if err != nil {
				continue
			}
			result.PRs = append(result.PRs, SearchResultPR{Owner: owner, Repo: repo, Number: number, Title: item.Title})
		}
		if len(resp.Items) < searchPerPage || page*searchPerPage >= resp.TotalCount {
			return result, nil
		}
	}
	if result.Total > len(result.PRs) {
		result.Incomplete = true
	}
	return result, nil
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
	var total, pending int

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
			total++
			st := NormalizeState(s.State)
			if st == "pending" {
				pending++
			}
			if st == "failure" || st == "error" {
				failed = append(failed, FailedCheck{Name: s.Context, URL: s.TargetURL})
			}
		}
	}

	for _, run := range runs {
		total++
		if NormalizeState(run.Status) != "completed" {
			hasPending = true
			pending++
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

	return &CIStatus{State: state, FailedChecks: failed, Total: total, Pending: pending}
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

	// GitHub rejects an empty "Bearer " with 401 even for public repos, so only send the
	// header when there is a token.
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	c.mu.Lock()
	cached, haveCached := c.cache[path]
	c.mu.Unlock()
	if haveCached {
		req.Header.Set("If-None-Match", cached.etag)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	c.recordRateLimit(resp.Header)

	var body []byte
	switch {
	case resp.StatusCode == http.StatusNotModified && haveCached:
		body = cached.body
	case resp.StatusCode == http.StatusOK:
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		if etag := resp.Header.Get("ETag"); etag != "" {
			c.storeCache(path, cachedResponse{etag: etag, body: body})
		}
	default:
		errBody, _ := io.ReadAll(resp.Body)
		return newAPIError(resp, errBody)
	}

	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	return nil
}

func (c *Client) storeCache(path string, entry cachedResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil || len(c.cache) >= maxCacheEntries {
		c.cache = make(map[string]cachedResponse)
	}
	c.cache[path] = entry
}

func (c *Client) recordRateLimit(h http.Header) {
	limit, errL := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	remaining, errR := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	reset, errS := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if errL != nil || errR != nil || errS != nil {
		return
	}
	c.mu.Lock()
	c.rateLimit = RateLimit{Limit: limit, Remaining: remaining, Reset: time.Unix(reset, 0), Known: true}
	c.mu.Unlock()
}

// newAPIError builds an APIError, detecting rate limits. GitHub signals them with 429,
// or with 403 plus either X-RateLimit-Remaining: 0 (primary limit) or a
// "rate limit" message (secondary limit); 403 alone usually means missing permissions.
func newAPIError(resp *http.Response, body []byte) *APIError {
	apiErr := &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return apiErr
	}
	exhausted := resp.Header.Get("X-RateLimit-Remaining") == "0"
	retryAfter, hasRetryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
	mentionsLimit := strings.Contains(strings.ToLower(apiErr.Body), "rate limit")
	if resp.StatusCode == http.StatusTooManyRequests || exhausted || hasRetryAfter || mentionsLimit {
		apiErr.RateLimited = true
		switch {
		case hasRetryAfter:
			apiErr.ResetAt = time.Now().Add(retryAfter)
		case exhausted:
			if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
				apiErr.ResetAt = time.Unix(reset, 0)
			}
		}
	}
	return apiErr
}

func parseRetryAfter(v string) (time.Duration, bool) {
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

// FormatPRURL constructs a GitHub PR URL.
func FormatPRURL(owner, repo string, number int) string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number)
}

// NormalizeState normalizes status state strings.
func NormalizeState(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}
