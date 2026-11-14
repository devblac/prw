// Package forge identifies pull requests across the code hosting services prw
// supports (GitHub, GitHub Enterprise and Bitbucket Cloud): parsing their URLs and
// building links.
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

// Ref identifies a pull request. Host is GitHub, Bitbucket, or a GitHub Enterprise
// Server / GHE.com host name; Owner is the GitHub owner or the Bitbucket workspace.
type Ref struct {
	Host   string
	Owner  string
	Repo   string
	Number int
}

var (
	bitbucketPRURL = regexp.MustCompile(`^(?:https?://)?(?:www\.)?bitbucket\.org/([^/]+)/([^/]+)/pull-requests/(\d+)`)
	// githubPRURL matches github.com and GitHub Enterprise pull request URLs.
	githubPRURL = regexp.MustCompile(`^(?:https?://)?([^/\s]+)/([^/]+)/([^/]+)/pull/(\d+)`)
)

// ParsePRURL parses a GitHub (github.com/owner/repo/pull/N), GitHub Enterprise
// (<host>/owner/repo/pull/N) or Bitbucket Cloud (bitbucket.org/workspace/repo/pull-requests/N)
// pull request URL. Whether prw can reach a GitHub Enterprise host is up to the
// caller (it needs github_enterprise_url).
func ParsePRURL(url string) (Ref, error) {
	url = strings.TrimSpace(url)
	if m := bitbucketPRURL.FindStringSubmatch(url); m != nil {
		return ref(Bitbucket, m[1], m[2], m[3])
	}
	if m := githubPRURL.FindStringSubmatch(url); m != nil && !IsBitbucket(m[1]) {
		return ref(NormalizeHost(m[1]), m[2], m[3], m[4])
	}
	return Ref{}, fmt.Errorf("not a GitHub (github.com/owner/repo/pull/N, or <enterprise host>/owner/repo/pull/N) or Bitbucket (bitbucket.org/workspace/repo/pull-requests/N) pull request URL")
}

func ref(host, owner, repo, number string) (Ref, error) {
	n, err := strconv.Atoi(number)
	if err != nil {
		return Ref{}, fmt.Errorf("invalid PR number: %w", err)
	}
	return Ref{Host: host, Owner: owner, Repo: repo, Number: n}, nil
}

// NormalizeHost maps a host to its canonical name: the empty host of watch-list
// entries created before Bitbucket support means GitHub; casing, a "www." prefix
// and a port-less trailing dot are dropped. Other hosts are GitHub Enterprise.
func NormalizeHost(host string) string {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	h = strings.TrimPrefix(h, "www.")
	switch h {
	case "", GitHub:
		return GitHub
	case Bitbucket:
		return Bitbucket
	}
	return h
}

// IsBitbucket reports whether host is Bitbucket Cloud.
func IsBitbucket(host string) bool {
	return NormalizeHost(host) == Bitbucket
}

// IsEnterprise reports whether host is a GitHub Enterprise host (neither github.com
// nor Bitbucket).
func IsEnterprise(host string) bool {
	h := NormalizeHost(host)
	return h != GitHub && h != Bitbucket
}

// PRURL is the web URL of a pull request.
func PRURL(host, owner, repo string, number int) string {
	if IsBitbucket(host) {
		return fmt.Sprintf("https://bitbucket.org/%s/%s/pull-requests/%d", owner, repo, number)
	}
	return fmt.Sprintf("https://%s/%s/%s/pull/%d", NormalizeHost(host), owner, repo, number)
}

// Display is the short form of a PR for lists: owner/repo#N, prefixed with
// "bitbucket:" on Bitbucket and with the host on GitHub Enterprise.
func Display(host, owner, repo string, number int) string {
	name := fmt.Sprintf("%s/%s#%d", owner, repo, number)
	switch h := NormalizeHost(host); {
	case h == Bitbucket:
		return "bitbucket:" + name
	case h != GitHub:
		return h + ":" + name
	}
	return name
}

// ServiceName names the service of host in messages: GitHub, GitHub Enterprise or
// Bitbucket.
func ServiceName(host string) string {
	switch {
	case IsBitbucket(host):
		return "Bitbucket"
	case IsEnterprise(host):
		return "GitHub Enterprise"
	}
	return "GitHub"
}

// URL is the web URL of the pull request.
func (r Ref) URL() string { return PRURL(r.Host, r.Owner, r.Repo, r.Number) }

// String is the short form "owner/repo#N".
func (r Ref) String() string { return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number) }

// StorageHost is how a host is stored in the watch list: empty for GitHub, so that
// existing config files (which predate the field) keep meaning GitHub.
func StorageHost(host string) string {
	if h := NormalizeHost(host); h != GitHub {
		return h
	}
	return ""
}
