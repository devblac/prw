package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/devblac/prw/internal/bitbucket"
	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
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
// works anonymously for public repositories.
func clientFor(cfg *config.Config, host string) (prClient, error) {
	if forge.IsBitbucket(host) {
		auth, _ := cfg.ResolveBitbucketAuth()
		return newBitbucketClient(auth.Email, auth.Token), nil
	}
	token := cfg.GetToken()
	if token == "" {
		return nil, errMissingToken
	}
	return newGitHubClient(token), nil
}

// needsGitHub reports whether the watch list or auto-watch involves GitHub.
func needsGitHub(cfg *config.Config) bool {
	if len(cfg.AutoWatch) > 0 {
		return true // auto-watch searches GitHub
	}
	for _, pr := range cfg.WatchedPRs {
		if !forge.IsBitbucket(pr.Host) {
			return true
		}
	}
	return len(cfg.WatchedPRs) == 0 // nothing yet: GitHub is the default
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
