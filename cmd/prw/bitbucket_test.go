package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devblac/prw/internal/bitbucket"
	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/forge"
)

const bbPRURL = "https://bitbucket.org/acme/api/pull-requests/42"

// fakeBitbucketAPI serves PR acme/api#42 with the given state and build statuses.
type fakeBitbucketAPI struct {
	state    string
	statuses string
	auth     string
}

func (f *fakeBitbucketAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.auth = r.Header.Get("Authorization")
	switch p := r.URL.Path; {
	case strings.HasSuffix(p, "/repositories/acme/api/pullrequests/42"):
		fmt.Fprintf(w, `{"id": 42, "title": "Add login", "state": %q, "updated_on": "2026-01-01T00:00:00+00:00",
			"source": {"commit": {"hash": "abc123def456"}},
			"participants": [{"user": {"uuid": "{a}"}, "role": "REVIEWER", "approved": true, "state": "approved"}]}`, f.state)
	case strings.HasSuffix(p, "/pullrequests/42/mergeability/checks"):
		fmt.Fprint(w, `{"values": [{"type": "git_mergeability_check", "status": "PASSED"}]}`)
	case strings.HasSuffix(p, "/commit/abc123def456/statuses"):
		fmt.Fprintf(w, `{"values": [%s]}`, f.statuses)
	default:
		http.NotFound(w, r)
	}
}

// setupBitbucket points Bitbucket requests at a fake API; GitHub has no token.
func setupBitbucket(t *testing.T, f *fakeBitbucketAPI, mutate func(*config.Config)) {
	t.Helper()
	setupWaitCommand(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected GitHub request %s", r.URL.Path)
		http.NotFound(w, r)
	})
	cfg, _ := config.Load()
	cfg.GitHubToken = ""
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("BITBUCKET_TOKEN", "")
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	old := newBitbucketClient
	newBitbucketClient = func(email, token string) *bitbucket.Client {
		c := bitbucket.NewClient(email, token)
		c.SetBaseURL(server.URL)
		return c
	}
	t.Cleanup(func() { newBitbucketClient = old })
}

func bbStatus(name, state string) string {
	return fmt.Sprintf(`{"key": %q, "name": %q, "state": %q, "url": "https://bitbucket.org/acme/api/pipelines/results/7",
		"created_on": "2026-01-01T00:00:00+00:00", "updated_on": "2026-01-01T00:04:00+00:00"}`, name, name, state)
}

func TestBitbucket_WatchListAndUnwatch(t *testing.T) {
	f := &fakeBitbucketAPI{state: "OPEN", statuses: bbStatus("Pipeline - default", "INPROGRESS")}
	setupBitbucket(t, f, func(c *config.Config) { c.BitbucketEmail, c.BitbucketToken = "dev@example.com", "api-token" })

	out, err := captureStdout(func() error { return watchCmd.RunE(watchCmd, []string{bbPRURL}) })
	if err != nil || !strings.Contains(out, "Now watching: acme/api#42 - Add login") {
		t.Fatalf("watch: %v\n%s", err, out)
	}
	if !strings.HasPrefix(f.auth, "Basic ") {
		t.Errorf("the API token must be sent: Authorization = %q", f.auth)
	}
	cfg, _ := config.Load()
	if len(cfg.WatchedPRs) != 1 || cfg.WatchedPRs[0].Host != forge.Bitbucket {
		t.Fatalf("watch list = %+v", cfg.WatchedPRs)
	}

	listJSON = false
	out, _ = captureStdout(func() error { return listCmd.RunE(listCmd, nil) })
	if !strings.Contains(out, "bitbucket:acme/api") {
		t.Errorf("list must show the host:\n%s", out)
	}

	// The same owner/repo#number on GitHub is a different PR.
	captureStdout(func() error { return unwatchCmd.RunE(unwatchCmd, []string{"https://github.com/acme/api/pull/42"}) })
	if cfg, _ := config.Load(); len(cfg.WatchedPRs) != 1 {
		t.Error("unwatching the GitHub PR must not remove the Bitbucket one")
	}
	captureStdout(func() error { return unwatchCmd.RunE(unwatchCmd, []string{bbPRURL}) })
	if cfg, _ := config.Load(); len(cfg.WatchedPRs) != 0 {
		t.Errorf("watch list = %+v", cfg.WatchedPRs)
	}
}

func TestBitbucket_Wait(t *testing.T) {
	f := &fakeBitbucketAPI{state: "OPEN", statuses: bbStatus("Pipeline - default", "FAILED") + "," + bbStatus("Jenkins", "SUCCESSFUL")}
	setupBitbucket(t, f, nil)
	waitJSON = true

	out, err := captureStdout(func() error { return waitCmd.RunE(waitCmd, []string{bbPRURL}) })
	if code := exitCode(err); code != exitWaitFailed {
		t.Fatalf("exit code = %d (err=%v)\n%s", code, err, out)
	}
	var result waitResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if result.Host != forge.Bitbucket || result.URL != bbPRURL || result.State != "failure" || result.MergeState != "ready" ||
		len(result.FailedChecks) != 1 || result.FailedChecks[0].Name != "Pipeline - default" {
		t.Errorf("result = %+v", result)
	}
}

func TestBitbucket_Checks(t *testing.T) {
	f := &fakeBitbucketAPI{state: "OPEN", statuses: bbStatus("Pipeline - default", "SUCCESSFUL")}
	setupBitbucket(t, f, nil)
	checksJSON, checksLogs = false, false

	out, err := captureStdout(func() error { return checksCmd.RunE(checksCmd, []string{bbPRURL}) })
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "✅ pass  Pipeline - default  4m00s  https://bitbucket.org/acme/api/pipelines/results/7") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestBitbucket_RunNotifiesWithBitbucketLinks(t *testing.T) {
	f := &fakeBitbucketAPI{state: "OPEN", statuses: bbStatus("Pipeline - default", "FAILED")}
	setupBitbucket(t, f, func(c *config.Config) {
		c.WatchedPRs = []config.WatchedPR{{Host: forge.Bitbucket, Owner: "acme", Repo: "api", Number: 42, LastKnownState: "pending"}}
	})
	runOnce = true
	t.Cleanup(func() { runOnce = false })

	out, err := captureStdout(func() error { return runCmd.RunE(runCmd, nil) })
	if err != nil {
		t.Fatalf("run without a GitHub token must work for Bitbucket-only watch lists: %v", err)
	}
	if !strings.Contains(out, "Status: pending → failure") || !strings.Contains(out, "Link: "+bbPRURL) {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestBitbucket_PrivateRepoHint(t *testing.T) {
	setupBitbucket(t, &fakeBitbucketAPI{state: "OPEN"}, nil)
	_, err := captureStdout(func() error {
		return watchCmd.RunE(watchCmd, []string{"https://bitbucket.org/acme/private/pull-requests/1"})
	})
	if err == nil || !strings.Contains(err.Error(), "prw config set bitbucket_token") {
		t.Errorf("err = %v; a 404 on Bitbucket must explain how to authenticate", err)
	}
}

func TestBitbucket_FlakyIsGitHubOnly(t *testing.T) {
	if _, _, err := parseRepoArg("https://bitbucket.org/acme/api/pull-requests/42"); err == nil || !strings.Contains(err.Error(), "GitHub Actions") {
		t.Errorf("err = %v", err)
	}
}
