package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Client is a simple GitHub API client.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewClient creates a new GitHub client.
func NewClient(token string) *Client {
	return &Client{
		BaseURL:    "https://api.github.com",
		Token:      token,
		HTTPClient: http.DefaultClient,
	}
}

// PullRequest represents a GitHub pull request.
type PullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Head   struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

// CombinedStatus represents the combined CI status for a commit.
type CombinedStatus struct {
	State string `json:"state"` // pending, success, failure, error
	SHA   string `json:"sha"`
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
	url := c.BaseURL + path

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var pr PullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &pr, nil
}

// GetCombinedStatus fetches the combined CI status for a commit.
func (c *Client) GetCombinedStatus(owner, repo, ref string) (*CombinedStatus, error) {
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/status", owner, repo, url.PathEscape(ref))
	url := c.BaseURL + path

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var status CombinedStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &status, nil
}

// FormatPRURL constructs a GitHub PR URL.
func FormatPRURL(owner, repo string, number int) string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number)
}

// NormalizeState normalizes status state strings.
func NormalizeState(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}
