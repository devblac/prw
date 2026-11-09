package config

import (
	"errors"
	"testing"
)

func TestEnterpriseHost(t *testing.T) {
	for in, want := range map[string]string{"": "", "https://GitHub.Acme.com/": "github.acme.com", "github.acme.com": "github.acme.com", "http://ghe.local:8080": "ghe.local:8080"} {
		if got := (&Config{GitHubEnterpriseURL: in}).EnterpriseHost(); got != want {
			t.Errorf("EnterpriseHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveEnterpriseToken(t *testing.T) {
	var askedHost string
	old := GHAuthTokenForHost
	GHAuthTokenForHost = func(host string) (string, error) {
		askedHost = host
		if host == "github.acme.com" {
			return "cli", nil
		}
		return "", errors.New("not logged in")
	}
	t.Cleanup(func() { GHAuthTokenForHost = old })

	cfg := &Config{GitHubEnterpriseURL: "https://github.acme.com", GitHubEnterpriseToken: "cfg"}
	t.Setenv("GH_ENTERPRISE_TOKEN", "env")
	t.Setenv("GITHUB_ENTERPRISE_TOKEN", "env2")
	if tok, src := cfg.ResolveEnterpriseToken(); tok != "cfg" || src != TokenSourceConfig {
		t.Errorf("config: %s %s", tok, src)
	}
	cfg.GitHubEnterpriseToken = ""
	if tok, _ := cfg.ResolveEnterpriseToken(); tok != "env" {
		t.Errorf("GH_ENTERPRISE_TOKEN: %s", tok)
	}
	t.Setenv("GH_ENTERPRISE_TOKEN", "")
	if tok, _ := cfg.ResolveEnterpriseToken(); tok != "env2" {
		t.Errorf("GITHUB_ENTERPRISE_TOKEN: %s", tok)
	}
	t.Setenv("GITHUB_ENTERPRISE_TOKEN", "")
	if tok, _ := cfg.ResolveEnterpriseToken(); tok != "cli" || askedHost != "github.acme.com" {
		t.Errorf("gh auth token: %s (asked for %s)", tok, askedHost)
	}
	if tok, src := (&Config{}).ResolveEnterpriseToken(); tok != "" || src != TokenSourceNone {
		t.Errorf("unset: %s %s", tok, src)
	}
	// GitHub.com's token is never used for the enterprise host, nor the other way round.
	t.Setenv("GITHUB_TOKEN", "dotcom")
	if tok, _ := (&Config{GitHubEnterpriseURL: "https://other.example.com"}).ResolveEnterpriseToken(); tok != "" {
		t.Errorf("must not fall back to github.com's token: %s", tok)
	}
}
