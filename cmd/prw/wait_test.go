package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
)

// pollStep is what the fake GitHub API returns during one poll.
type pollStep struct {
	pr     *github.PullRequest
	prErr  error
	status *github.CombinedStatus
	runs   []github.CheckRun
	ciErr  error
	// movedTo, if set, is what the PR looks like when waitForCI re-reads it to
	// confirm the head hasn't moved, i.e. a push that landed during this poll.
	movedTo *github.PullRequest
}

// scriptedClient returns steps[i] on the i-th poll (repeating the last one). A PR
// lookup right after a poll that found a final CI result is the head re-check, not a
// new poll.
type scriptedClient struct {
	steps         []pollStep
	polls         int
	revalidations int
	current       pollStep
	afterCI       bool
}

func (c *scriptedClient) GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error) {
	if c.afterCI {
		c.afterCI = false
		status := c.current.status
		if status == nil {
			status = &github.CombinedStatus{State: "pending"}
		}
		ci := github.SummarizeCI(status, c.current.runs)
		github.ApplyNoCI(ci, c.current.pr.UpdatedAt, time.Now())
		if ci.State != "pending" {
			c.revalidations++
			if c.current.movedTo != nil {
				return c.current.movedTo, nil
			}
			return c.current.pr, nil
		}
	}
	i := c.polls
	if i >= len(c.steps) {
		i = len(c.steps) - 1
	}
	c.polls++
	c.current = c.steps[i]
	return c.current.pr, c.current.prErr
}

func (c *scriptedClient) GetCombinedStatus(ctx context.Context, owner, repo, ref string) (*github.CombinedStatus, error) {
	if c.current.ciErr != nil {
		return nil, c.current.ciErr
	}
	if c.current.status != nil {
		return c.current.status, nil
	}
	return &github.CombinedStatus{State: "pending"}, nil
}

func (c *scriptedClient) GetCheckRuns(ctx context.Context, owner, repo, ref string) ([]github.CheckRun, error) {
	c.afterCI = true
	return c.current.runs, nil
}

func openPR(sha string) *github.PullRequest {
	pr := &github.PullRequest{Number: 1, Title: "Add feature", State: "open"}
	pr.Head.SHA = sha
	return pr
}

func run(name, status, conclusion string) github.CheckRun {
	return github.CheckRun{Name: name, Status: status, Conclusion: conclusion, HTMLURL: "https://github.com/o/r/runs/" + name}
}

// noSleep makes waitForCI poll without delay for the duration of a test.
func noSleep(t *testing.T) *int {
	t.Helper()
	sleeps := 0
	old := sleepFunc
	sleepFunc = func(ctx context.Context, d time.Duration) { sleeps++ }
	t.Cleanup(func() { sleepFunc = old })
	return &sleeps
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return -1
}

func TestWaitForCI_PendingThenSuccess(t *testing.T) {
	noSleep(t)
	client := &scriptedClient{steps: []pollStep{
		{pr: openPR("aaa1111"), runs: nil}, // CI not started yet
		{pr: openPR("aaa1111"), runs: []github.CheckRun{run("Test", "in_progress", ""), run("Build", "queued", "")}},
		{pr: openPR("aaa1111"), runs: []github.CheckRun{run("Test", "in_progress", ""), run("Build", "queued", "")}},
		{pr: openPR("aaa1111"), runs: []github.CheckRun{run("Test", "completed", "success"), run("Build", "completed", "success")}},
	}}
	var progress bytes.Buffer

	result, err := waitForCI(context.Background(), client, "o", "r", 1, time.Second, &progress)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "success" || result.TotalChecks != 2 || result.PendingChecks != 0 {
		t.Errorf("result = %+v", result)
	}
	if code := exitCode(result.exitErr()); code != exitWaitSuccess {
		t.Errorf("exit code = %d, want 0", code)
	}
	if client.polls != 4 {
		t.Errorf("polls = %d, want 4", client.polls)
	}

	out := progress.String()
	if !strings.Contains(out, "waiting for CI to start on aaa1111") {
		t.Errorf("progress should say CI has not started:\n%s", out)
	}
	if got := strings.Count(out, "2 of 2 check(s) still running"); got != 1 {
		t.Errorf("unchanged progress must be printed once, got %d times:\n%s", got, out)
	}
}

func TestWaitForCI_FailsFast(t *testing.T) {
	noSleep(t)
	client := &scriptedClient{steps: []pollStep{
		{pr: openPR("sha"), runs: []github.CheckRun{run("Lint", "completed", "failure"), run("E2E", "in_progress", "")}},
	}}

	result, err := waitForCI(context.Background(), client, "o", "r", 1, time.Second, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "failure" || len(result.FailedChecks) != 1 || result.FailedChecks[0].Name != "Lint" {
		t.Errorf("result = %+v", result)
	}
	if client.polls != 1 {
		t.Errorf("must not keep waiting for E2E once Lint failed, polls = %d", client.polls)
	}
	if code := exitCode(result.exitErr()); code != exitWaitFailed {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestWaitForCI_LegacyErrorExitsAsFailure(t *testing.T) {
	noSleep(t)
	client := &scriptedClient{steps: []pollStep{
		{pr: openPR("sha"), status: &github.CombinedStatus{State: "error", TotalCount: 1, Statuses: []github.CommitStatus{{Context: "ci/jenkins", State: "error"}}}},
	}}

	result, err := waitForCI(context.Background(), client, "o", "r", 1, time.Second, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if code := exitCode(result.exitErr()); code != exitWaitFailed {
		t.Errorf("exit code = %d, want 1 for state %q", code, result.State)
	}
}

func TestWaitForCI_MergedWhileWaiting(t *testing.T) {
	noSleep(t)
	merged := openPR("sha")
	merged.State, merged.Merged = "closed", true
	client := &scriptedClient{steps: []pollStep{
		{pr: openPR("sha"), runs: []github.CheckRun{run("Test", "in_progress", "")}},
		{pr: merged},
	}}

	result, err := waitForCI(context.Background(), client, "o", "r", 1, time.Second, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "merged" {
		t.Errorf("state = %q, want merged", result.State)
	}
	if code := exitCode(result.exitErr()); code != exitWaitSuccess {
		t.Errorf("a merge (e.g. auto-merge) should exit 0, got %d", code)
	}
}

func TestWaitForCI_ClosedWithoutMerge(t *testing.T) {
	noSleep(t)
	closed := openPR("sha")
	closed.State = "closed"
	client := &scriptedClient{steps: []pollStep{{pr: closed}}}

	result, err := waitForCI(context.Background(), client, "o", "r", 1, time.Second, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if code := exitCode(result.exitErr()); code != exitWaitError {
		t.Errorf("exit code = %d, want 3", code)
	}
}

func TestWaitForCI_FollowsNewHeadCommit(t *testing.T) {
	noSleep(t)
	client := &scriptedClient{steps: []pollStep{
		{pr: openPR("old0000"), runs: []github.CheckRun{run("Test", "in_progress", "")}},
		{pr: openPR("new1111"), runs: []github.CheckRun{run("Test", "completed", "success")}},
	}}

	result, err := waitForCI(context.Background(), client, "o", "r", 1, time.Second, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if result.SHA != "new1111" || result.State != "success" {
		t.Errorf("result = %+v, want success on new1111", result)
	}
}

func TestWaitForCI_Timeout(t *testing.T) {
	old := sleepFunc
	sleepFunc = func(ctx context.Context, d time.Duration) { <-ctx.Done() }
	defer func() { sleepFunc = old }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	client := &scriptedClient{steps: []pollStep{{pr: openPR("sha"), runs: []github.CheckRun{run("Test", "in_progress", "")}}}}

	result, err := waitForCI(ctx, client, "o", "r", 1, time.Hour, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TimedOut || result.State != "pending" {
		t.Errorf("result = %+v, want timed out while pending", result)
	}
	if code := exitCode(result.exitErr()); code != exitWaitTimeout {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestWaitForCI_Interrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	old := sleepFunc
	sleepFunc = func(context.Context, time.Duration) { cancel() }
	defer func() { sleepFunc = old }()
	client := &scriptedClient{steps: []pollStep{{pr: openPR("sha"), runs: []github.CheckRun{run("Test", "queued", "")}}}}

	result, err := waitForCI(ctx, client, "o", "r", 1, time.Hour, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if code := exitCode(result.exitErr()); code != exitWaitInterrupted {
		t.Errorf("exit code = %d, want 130", code)
	}
}

func TestWaitForCI_RetriesTransientErrors(t *testing.T) {
	noSleep(t)
	client := &scriptedClient{steps: []pollStep{
		{prErr: errors.New("connection reset by peer")},
		{prErr: &github.APIError{StatusCode: http.StatusBadGateway}},
		{pr: openPR("sha"), runs: []github.CheckRun{run("Test", "completed", "success")}},
	}}
	var progress bytes.Buffer

	result, err := waitForCI(context.Background(), client, "o", "r", 1, time.Second, &progress)
	if err != nil {
		t.Fatalf("transient errors should be retried, got %v", err)
	}
	if result.State != "success" {
		t.Errorf("state = %q", result.State)
	}
	if strings.Count(progress.String(), "retrying") != 2 {
		t.Errorf("expected two retry warnings:\n%s", progress.String())
	}
}

func TestWaitForCI_WaitsForRateLimitResetWithoutGivingUp(t *testing.T) {
	var delays []time.Duration
	old := sleepFunc
	sleepFunc = func(ctx context.Context, d time.Duration) { delays = append(delays, d) }
	defer func() { sleepFunc = old }()

	limited := &github.APIError{StatusCode: 403, RateLimited: true, ResetAt: time.Now().Add(10 * time.Minute)}
	client := &scriptedClient{steps: []pollStep{
		// More rate-limit hits in a row than the transient-error budget allows.
		{prErr: limited}, {prErr: limited}, {prErr: limited}, {prErr: limited},
		{pr: openPR("sha"), runs: []github.CheckRun{run("Test", "completed", "success")}},
	}}
	var progress bytes.Buffer

	result, err := waitForCI(context.Background(), client, "o", "r", 1, 15*time.Second, &progress)
	if err != nil {
		t.Fatalf("rate limits must not count as errors, got %v", err)
	}
	if result.State != "success" {
		t.Errorf("state = %q", result.State)
	}
	if len(delays) != 4 || delays[0] < 9*time.Minute {
		t.Errorf("expected 4 waits of ~10m until the reset, got %v", delays)
	}
	if !strings.Contains(progress.String(), "rate limit reached") {
		t.Errorf("expected a rate limit message:\n%s", progress.String())
	}
}

func TestWaitForCI_RateLimitWithoutResetUsesInterval(t *testing.T) {
	var delays []time.Duration
	old := sleepFunc
	sleepFunc = func(ctx context.Context, d time.Duration) { delays = append(delays, d) }
	defer func() { sleepFunc = old }()

	client := &scriptedClient{steps: []pollStep{
		{prErr: &github.APIError{StatusCode: 429, RateLimited: true}},
		{pr: openPR("sha"), runs: []github.CheckRun{run("Test", "completed", "success")}},
	}}
	if _, err := waitForCI(context.Background(), client, "o", "r", 1, 15*time.Second, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(delays) != 1 || delays[0] != 15*time.Second {
		t.Errorf("expected one wait of the poll interval, got %v", delays)
	}
}

func TestWaitForCI_GivesUpAfterConsecutiveTransientErrors(t *testing.T) {
	noSleep(t)
	client := &scriptedClient{steps: []pollStep{{prErr: errors.New("no route to host")}}}

	_, err := waitForCI(context.Background(), client, "o", "r", 1, time.Second, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "3 consecutive errors") {
		t.Errorf("expected to give up after 3 errors, got %v", err)
	}
}

func TestWaitForCI_PermanentErrorsAreNotRetried(t *testing.T) {
	tests := []struct {
		name string
		step pollStep
	}{
		{"PR not found", pollStep{prErr: &github.APIError{StatusCode: http.StatusNotFound}}},
		{"bad credentials", pollStep{prErr: &github.APIError{StatusCode: http.StatusUnauthorized}}},
		{"missing Checks permission", pollStep{pr: openPR("sha"), ciErr: &github.APIError{StatusCode: http.StatusForbidden, Body: "Resource not accessible by personal access token"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			noSleep(t)
			client := &scriptedClient{steps: []pollStep{tt.step}}
			if _, err := waitForCI(context.Background(), client, "o", "r", 1, time.Second, &bytes.Buffer{}); err == nil {
				t.Fatal("expected an error")
			}
			if client.polls != 1 {
				t.Errorf("a permanent error must not be retried, polls = %d", client.polls)
			}
		})
	}
}

func TestIsTransient(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{errors.New("dial tcp: timeout"), true},
		{&github.APIError{StatusCode: 500}, true},
		{&github.APIError{StatusCode: 503}, true},
		{&github.APIError{StatusCode: 429}, true},
		{&github.APIError{StatusCode: 403, RateLimited: true}, true}, // detection is tested in the github package
		{&github.APIError{StatusCode: 403, Body: `{"message":"Resource not accessible"}`}, false},
		{&github.APIError{StatusCode: 404}, false},
		{&github.APIError{StatusCode: 401}, false},
		{fmt.Errorf("wrapped: %w", &github.APIError{StatusCode: 404}), false},
	}
	for _, tt := range tests {
		if got := isTransient(tt.err); got != tt.want {
			t.Errorf("isTransient(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestPrintWaitResult(t *testing.T) {
	result := &waitResult{
		Owner: "o", Repo: "r", Number: 7, Title: "Fix bug", URL: "https://github.com/o/r/pull/7",
		SHA: "abc", State: "failure", TotalChecks: 3, ElapsedSeconds: 75,
		FailedChecks: []github.FailedCheck{{Name: "Test", URL: "https://github.com/o/r/runs/1"}, {Name: "Lint"}},
	}

	var human bytes.Buffer
	if err := printWaitResult(&human, result, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"❌ o/r#7 — Fix bug", "CI: failure (3 check(s), 1m15s)", "❌ Test  https://github.com/o/r/runs/1", "❌ Lint", "https://github.com/o/r/pull/7"} {
		if !strings.Contains(human.String(), want) {
			t.Errorf("human output missing %q:\n%s", want, human.String())
		}
	}

	var out bytes.Buffer
	if err := printWaitResult(&out, result, true); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if decoded["state"] != "failure" || decoded["number"] != float64(7) || len(decoded["failed_checks"].([]interface{})) != 2 {
		t.Errorf("unexpected JSON: %s", out.String())
	}
	if _, ok := decoded["timed_out"]; ok {
		t.Errorf("timed_out should be omitted when false: %s", out.String())
	}
}

func TestPrintWaitResult_TimeoutAndMerged(t *testing.T) {
	var buf bytes.Buffer
	printWaitResult(&buf, &waitResult{Owner: "o", Repo: "r", Number: 1, State: "pending", TimedOut: true, ElapsedSeconds: 600}, false)
	if !strings.Contains(buf.String(), "⏳") || !strings.Contains(buf.String(), "Timed out after 10m0s; CI is still pending") {
		t.Errorf("unexpected timeout output:\n%s", buf.String())
	}

	buf.Reset()
	printWaitResult(&buf, &waitResult{Owner: "o", Repo: "r", Number: 1, State: "merged"}, false)
	if !strings.Contains(buf.String(), "✅") || !strings.Contains(buf.String(), "PR was merged") {
		t.Errorf("unexpected merged output:\n%s", buf.String())
	}
}

// setupWaitCommand points the command at a fake GitHub server and a config with a token.
func setupWaitCommand(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".prw", "config.json")
	oldPath := config.ConfigPath
	config.ConfigPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { config.ConfigPath = oldPath })
	cfg := config.DefaultConfig()
	cfg.GitHubToken = "test-token"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	oldClient := newGitHubClient
	newGitHubClient = func(token string) *github.Client {
		c := github.NewClient(token)
		c.BaseURL = server.URL
		c.HTTPClient = server.Client()
		return c
	}
	t.Cleanup(func() { newGitHubClient = oldClient })

	noSleep(t)
	oldTimeout, oldInterval, oldJSON := waitTimeout, waitInterval, waitJSON
	waitTimeout, waitInterval, waitJSON = time.Minute, time.Second, false
	t.Cleanup(func() { waitTimeout, waitInterval, waitJSON = oldTimeout, oldInterval, oldJSON })
}

func TestWaitCmd_ExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		conclusion string
		wantCode   int
		wantOut    string
	}{
		{"success", "success", exitWaitSuccess, "✅ owner/repo#1 — Add feature"},
		{"failure", "failure", exitWaitFailed, "❌ Test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var checkRunCalls int32
			setupWaitCommand(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/pulls/1"):
					fmt.Fprint(w, `{"number":1,"title":"Add feature","state":"open","head":{"sha":"abc1234"}}`)
				case strings.HasSuffix(r.URL.Path, "/status"):
					fmt.Fprint(w, `{"state":"pending","total_count":0,"statuses":[]}`)
				case strings.HasSuffix(r.URL.Path, "/check-runs"):
					// First poll: still running. Second poll: finished.
					if atomic.AddInt32(&checkRunCalls, 1) == 1 {
						fmt.Fprint(w, `{"total_count":1,"check_runs":[{"name":"Test","status":"in_progress"}]}`)
						return
					}
					fmt.Fprintf(w, `{"total_count":1,"check_runs":[{"name":"Test","status":"completed","conclusion":%q}]}`, tt.conclusion)
				default:
					http.NotFound(w, r)
				}
			})

			out, err := captureStdout(func() error {
				return waitCmd.RunE(waitCmd, []string{"https://github.com/owner/repo/pull/1"})
			})
			if code := exitCode(err); code != tt.wantCode {
				t.Errorf("exit code = %d (err=%v), want %d", code, err, tt.wantCode)
			}
			if !strings.Contains(out, tt.wantOut) {
				t.Errorf("stdout missing %q:\n%s", tt.wantOut, out)
			}
		})
	}
}

func TestWaitCmd_JSONOutput(t *testing.T) {
	setupWaitCommand(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/1"):
			fmt.Fprint(w, `{"number":1,"title":"Add feature","state":"open","head":{"sha":"abc1234"}}`)
		case strings.HasSuffix(r.URL.Path, "/status"):
			fmt.Fprint(w, `{"state":"success","total_count":1,"statuses":[{"context":"ci","state":"success"}]}`)
		default:
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		}
	})
	waitJSON = true

	out, err := captureStdout(func() error {
		return waitCmd.RunE(waitCmd, []string{"https://github.com/owner/repo/pull/1"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result waitResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("stdout must be pure JSON with --json: %v\n%s", err, out)
	}
	if result.State != "success" || result.SHA != "abc1234" || result.URL != "https://github.com/owner/repo/pull/1" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestWaitCmd_InputErrors(t *testing.T) {
	setupWaitCommand(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no API call expected, got %s", r.URL.Path)
	})

	if code := exitCode(waitCmd.Args(waitCmd, nil)); code != exitWaitError {
		t.Errorf("missing argument: exit code = %d, want 3", code)
	}
	if code := exitCode(waitCmd.Args(waitCmd, []string{"a", "b"})); code != exitWaitError {
		t.Errorf("two arguments: exit code = %d, want 3", code)
	}
	if code := exitCode(waitCmd.RunE(waitCmd, []string{"not-a-pr-url"})); code != exitWaitError {
		t.Errorf("invalid URL: exit code = %d, want 3", code)
	}

	waitInterval = 0
	if code := exitCode(waitCmd.RunE(waitCmd, []string{"https://github.com/o/r/pull/1"})); code != exitWaitError {
		t.Errorf("zero interval: exit code = %d, want 3", code)
	}
	waitInterval = time.Second
	waitTimeout = -time.Second
	if code := exitCode(waitCmd.RunE(waitCmd, []string{"https://github.com/o/r/pull/1"})); code != exitWaitError {
		t.Errorf("negative timeout: exit code = %d, want 3", code)
	}
}

func TestWaitCmd_MissingToken(t *testing.T) {
	setupWaitCommand(t, func(w http.ResponseWriter, r *http.Request) {})
	if err := config.Update(func(c *config.Config) error { c.GitHubToken = ""; return nil }); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "")

	err := waitCmd.RunE(waitCmd, []string{"https://github.com/o/r/pull/1"})
	if code := exitCode(err); code != exitWaitError || !errors.Is(err, errMissingToken) {
		t.Errorf("err = %v (code %d), want errMissingToken with code 3", err, code)
	}
}

func TestWaitCmd_APIErrorExits3(t *testing.T) {
	setupWaitCommand(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	})

	err := waitCmd.RunE(waitCmd, []string{"https://github.com/o/r/pull/1"})
	if code := exitCode(err); code != exitWaitError || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v (code %d), want a 404 with exit code 3", err, code)
	}
}
