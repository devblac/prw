package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
)

func TestClientFor_Enterprise(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		fmt.Fprint(w, `{"number": 9, "title": "Add SSO", "state": "open", "head": {"sha": "abc"}}`)
	}))
	defer srv.Close()
	old := newEnterpriseClient
	newEnterpriseClient = func(webURL, token string) (*github.Client, error) {
		c, err := github.NewEnterpriseClient(webURL, token)
		if err == nil {
			c.BaseURL = srv.URL + "/api/v3"
		}
		return c, err
	}
	t.Cleanup(func() { newEnterpriseClient = old })
	t.Setenv("GH_ENTERPRISE_TOKEN", "ghe-token")
	t.Setenv("GITHUB_ENTERPRISE_TOKEN", "")

	ref, err := forge.ParsePRURL("https://github.acme.com/team/api/pull/9")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clientFor(&config.Config{}, ref.Host); err == nil || !strings.Contains(err.Error(), "github_enterprise_url") {
		t.Errorf("unconfigured host: err = %v", err)
	}
	if _, err := clientFor(&config.Config{GitHubEnterpriseURL: "https://other.example.com"}, ref.Host); err == nil {
		t.Error("a host other than the configured one must be refused")
	}

	cfg := &config.Config{GitHubEnterpriseURL: "https://github.acme.com"}
	client, err := clientFor(cfg, ref.Host)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := client.GetPullRequest(context.Background(), ref.Owner, ref.Repo, ref.Number)
	if err != nil || pr.Title != "Add SSO" {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
	if gotPath != "/api/v3/repos/team/api/pulls/9" || gotAuth != "Bearer ghe-token" {
		t.Errorf("request to %s with %q", gotPath, gotAuth)
	}

	t.Setenv("GH_ENTERPRISE_TOKEN", "")
	old2 := config.GHAuthTokenForHost
	config.GHAuthTokenForHost = func(string) (string, error) { return "", fmt.Errorf("not logged in") }
	t.Cleanup(func() { config.GHAuthTokenForHost = old2 })
	if _, err := clientFor(cfg, ref.Host); err == nil || !strings.Contains(err.Error(), "gh auth login --hostname github.acme.com") {
		t.Errorf("missing token: err = %v", err)
	}
}

func TestConfigSet_EnterpriseURL(t *testing.T) {
	cfg := &config.Config{}
	if err := applyConfigSet(cfg, "github_enterprise_url", "https://github.acme.com/"); err != nil || cfg.GitHubEnterpriseURL != "https://github.acme.com" {
		t.Errorf("set: %v, %q", err, cfg.GitHubEnterpriseURL)
	}
	for _, bad := range []string{"https://github.com", "bitbucket.org", "ftp://x.example.com"} {
		if err := applyConfigSet(cfg, "github_enterprise_url", bad); err == nil {
			t.Errorf("github_enterprise_url %q should be refused", bad)
		}
	}
	if err := applyConfigUnset(cfg, "github_enterprise_url"); err != nil || cfg.GitHubEnterpriseURL != "" {
		t.Errorf("unset: %v", err)
	}
}

func TestNeedsGitHub(t *testing.T) {
	t.Setenv("BITBUCKET_TOKEN", "")
	t.Setenv("BITBUCKET_EMAIL", "")
	mine := []string{config.AutoWatchMine}
	ghPR := []config.WatchedPR{{Owner: "o", Repo: "r", Number: 1}}
	bbPR := []config.WatchedPR{{Host: forge.Bitbucket, Owner: "o", Repo: "r", Number: 1}}
	ghe := "https://github.acme.com"

	for _, tt := range []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{"nothing configured", config.Config{}, true},
		{"auto-watch with no other account", config.Config{AutoWatch: mine}, true},
		{"github.com PR always needs it", config.Config{WatchedPRs: ghPR, BitbucketToken: "t"}, true},
		{"only Bitbucket PRs", config.Config{WatchedPRs: bbPR}, false},
		{"Bitbucket auto-watch, empty list", config.Config{AutoWatch: mine, BitbucketToken: "t"}, false},
		{"Enterprise auto-watch, empty list", config.Config{AutoWatch: mine, GitHubEnterpriseURL: ghe}, false},
		{"Enterprise account, empty list", config.Config{GitHubEnterpriseURL: ghe}, false},
	} {
		cfg := tt.cfg
		if got := needsGitHub(&cfg); got != tt.want {
			t.Errorf("%s: needsGitHub = %v, want %v", tt.name, got, tt.want)
		}
	}
}
