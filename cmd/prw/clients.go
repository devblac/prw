package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/devblac/prw/internal/bitbucket"
	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
	"github.com/devblac/prw/internal/watcher"
)

// prClient is everything the commands use from a code host's client. Both the
// GitHub and the Bitbucket clients implement it.
type prClient interface {
	ciClient
	github.LogFetcher
	github.FlakyFetcher
	GetReviews(ctx context.Context, owner, repo string, number int) ([]github.Review, error)
}

// newBitbucketClient allows tests to point Bitbucket requests at a fake server.
var newBitbucketClient = func(email, token string) *bitbucket.Client {
	return bitbucket.NewClient(email, token)
}

// clientFor returns the client for PRs on host. GitHub needs a token; Bitbucket
// works anonymously for public repositories; a GitHub Enterprise host must be the
// one in github_enterprise_url.
func clientFor(cfg *config.Config, host string) (prClient, error) {
	switch {
	case forge.IsBitbucket(host):
		auth, _ := cfg.ResolveBitbucketAuth()
		return newBitbucketClient(auth.Email, auth.Token), nil
	case forge.IsEnterprise(host):
		return enterpriseClient(cfg, host)
	}
	token := cfg.GetToken()
	if token == "" {
		return nil, errMissingToken
	}
	return newGitHubClient(token), nil
}

// newEnterpriseClient allows tests to point GitHub Enterprise requests at a fake server.
var newEnterpriseClient = github.NewEnterpriseClient

// enterpriseClient returns the client of the GitHub Enterprise instance on host.
func enterpriseClient(cfg *config.Config, host string) (*github.Client, error) {
	configured := cfg.EnterpriseHost()
	if configured == "" || forge.NormalizeHost(host) != forge.NormalizeHost(configured) {
		return nil, fmt.Errorf("%s is not a known host: for GitHub Enterprise, run 'prw config set github_enterprise_url https://%s'", host, host)
	}
	token, _ := cfg.ResolveEnterpriseToken()
	if token == "" {
		return nil, fmt.Errorf("missing GitHub Enterprise token for %s: run 'gh auth login --hostname %s', set GH_ENTERPRISE_TOKEN, or 'prw config set github_enterprise_token <token>'", host, host)
	}
	return newEnterpriseClient(cfg.GitHubEnterpriseURL, token)
}

// needsGitHub reports whether the watch list or auto-watch involves github.com.
func needsGitHub(cfg *config.Config) bool {
	if len(cfg.AutoWatch) > 0 && !bitbucketConfigured(cfg) && cfg.EnterpriseHost() == "" {
		return true // auto-watch has nothing else to search
	}
	for _, pr := range cfg.WatchedPRs {
		if forge.NormalizeHost(pr.Host) == forge.GitHub {
			return true
		}
	}
	return len(cfg.WatchedPRs) == 0 // nothing yet: GitHub is the default
}

// bitbucketConfigured reports whether prw knows who you are on Bitbucket, which
// finding your PRs there needs.
func bitbucketConfigured(cfg *config.Config) bool {
	auth, _ := cfg.ResolveBitbucketAuth()
	return auth.Token != "" || cfg.BitbucketUser != ""
}

// autoWatchFinders returns the auto-watch finders of the configured accounts; bb is
// the Bitbucket client to use.
func autoWatchFinders(cfg *config.Config, bb *bitbucket.Client) []watcher.Finder {
	var finders []watcher.Finder
	if token := cfg.GetToken(); token != "" {
		finders = append(finders, watcher.GitHubFinder{Searcher: newGitHubClient(token)})
	}
	if f := enterpriseFinder(cfg); f != nil {
		finders = append(finders, f)
	}
	if f := bitbucketFinder(cfg, bb); f != nil {
		finders = append(finders, f)
	}
	return finders
}

// enterpriseFinder returns the GitHub Enterprise auto-watch finder, or nil without
// a usable github_enterprise_url and token.
func enterpriseFinder(cfg *config.Config) watcher.Finder {
	host := cfg.EnterpriseHost()
	if host == "" {
		return nil
	}
	c, err := enterpriseClient(cfg, host)
	if err != nil {
		return nil
	}
	return watcher.GitHubFinder{Searcher: c, HostName: host}
}

// bitbucketFinder returns the Bitbucket auto-watch finder, or nil without a Bitbucket
// account.
func bitbucketFinder(cfg *config.Config, bb *bitbucket.Client) *watcher.BitbucketFinder {
	if !bitbucketConfigured(cfg) {
		return nil
	}
	return &watcher.BitbucketFinder{API: bb, User: cfg.BitbucketUser, Workspaces: cfg.BitbucketWorkspaces}
}

// bitbucketAuthHint explains how to authenticate when Bitbucket refuses a request:
// it answers 404 rather than 401/403 for private repositories without access.
func bitbucketAuthHint(host string, err error) string {
	var apiErr *github.APIError
	if !forge.IsBitbucket(host) || !errors.As(err, &apiErr) {
		return ""
	}
	switch apiErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return " (for private repositories set a Bitbucket API token: 'prw config set bitbucket_email <you@example.com>' and 'prw config set bitbucket_token <token>', or BITBUCKET_EMAIL / BITBUCKET_TOKEN)"
	}
	return ""
}
