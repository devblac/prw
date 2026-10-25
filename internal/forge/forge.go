// Package forge identifies pull requests across the code hosting services prw
// supports (GitHub and Bitbucket Cloud): parsing their URLs and building links.
package forge

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Hosts prw supports.
const (
	GitHub    = "github.com"
	Bitbucket = "bitbucket.org"
)

// Ref identifies a pull request. Host is GitHub or Bitbucket; Owner is the GitHub
// owner or the Bitbucket workspace.
type Ref struct {
	Host   string
	Owner  string
	Repo   string
	Number int
}

var (
	githubPRURL    = regexp.MustCompile(`^(?:https?://)?(?:www\.)?github\.com/([^/]+)/([^/]+)/pull/(\d+)`)
	bitbucketPRURL = regexp.MustCompile(`^(?:https?://)?(?:www\.)?bitbucket\.org/([^/]+)/([^/]+)/pull-requests/(\d+)`)
)

// ParsePRURL parses a GitHub (github.com/owner/repo/pull/N) or Bitbucket Cloud
// (bitbucket.org/workspace/repo/pull-requests/N) pull request URL.
func ParsePRURL(url string) (Ref, error) {
	url = strings.TrimSpace(url)
	for host, re := range map[string]*regexp.Regexp{GitHub: githubPRURL, Bitbucket: bitbucketPRURL} {
		if m := re.FindStringSubmatch(url); m != nil {
			n, err := strconv.Atoi(m[3])
			if err != nil {
				return Ref{}, fmt.Errorf("invalid PR number: %w", err)
			}
			return Ref{Host: host, Owner: m[1], Repo: m[2], Number: n}, nil
		}
	}
	return Ref{}, fmt.Errorf("not a GitHub (github.com/owner/repo/pull/N) or Bitbucket (bitbucket.org/workspace/repo/pull-requests/N) pull request URL")
}

// NormalizeHost maps the empty host of watch-list entries created before Bitbucket
// support (and any casing) to a known host.
func NormalizeHost(host string) string {
	if strings.EqualFold(strings.TrimSpace(host), Bitbucket) {
		return Bitbucket
	}
	return GitHub
}

// IsBitbucket reports whether host is Bitbucket Cloud.
func IsBitbucket(host string) bool {
	return NormalizeHost(host) == Bitbucket
}

// PRURL is the web URL of a pull request.
func PRURL(host, owner, repo string, number int) string {
	if IsBitbucket(host) {
		return fmt.Sprintf("https://bitbucket.org/%s/%s/pull-requests/%d", owner, repo, number)
	}
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number)
}

// URL is the web URL of the pull request.
func (r Ref) URL() string { return PRURL(r.Host, r.Owner, r.Repo, r.Number) }

// String is the short form "owner/repo#N".
func (r Ref) String() string { return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number) }

// StorageHost is how a host is stored in the watch list: empty for GitHub, so that
// existing config files (which predate the field) keep meaning GitHub.
func StorageHost(host string) string {
	if IsBitbucket(host) {
		return Bitbucket
	}
	return ""
}
