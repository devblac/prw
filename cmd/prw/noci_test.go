package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
)

func staleOpenPR(sha string, quietFor time.Duration) *github.PullRequest {
	pr := openPR(sha)
	pr.UpdatedAt = time.Now().Add(-quietFor)
	return pr
}

func TestWaitForCI_NoCIStopsInsteadOfWaitingForTheTimeout(t *testing.T) {
	noSleep(t)
	client := &scriptedClient{steps: []pollStep{{pr: staleOpenPR("sha", time.Hour)}}}

	result, err := waitForCI(context.Background(), client, forge.Ref{Owner: "o", Repo: "r", Number: 1}, time.Second, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != github.StateNoCI || client.polls != 1 {
		t.Errorf("state=%q polls=%d, want no_ci after one poll", result.State, client.polls)
	}
	if code := exitCode(result.exitErr()); code != exitWaitNoCI {
		t.Errorf("exit code = %d, want 4 (no CI must not look like success)", code)
	}
}

func TestWaitForCI_WaitsForCIToRegisterAfterAPush(t *testing.T) {
	noSleep(t)
	client := &scriptedClient{steps: []pollStep{
		{pr: staleOpenPR("new", 10*time.Second)}, // just pushed, nothing registered yet
		{pr: staleOpenPR("new", 20*time.Second), runs: []github.CheckRun{run("Test", "queued", "")}},
		{pr: staleOpenPR("new", 30*time.Second), runs: []github.CheckRun{run("Test", "completed", "success")}},
	}}

	result, err := waitForCI(context.Background(), client, forge.Ref{Owner: "o", Repo: "r", Number: 1}, time.Second, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "success" || client.polls != 3 {
		t.Errorf("state=%q polls=%d, want success after 3 polls", result.State, client.polls)
	}
}

func TestPrintWaitResult_NoCI(t *testing.T) {
	var buf bytes.Buffer
	printWaitResult(&buf, &waitResult{Owner: "o", Repo: "r", Number: 1, State: github.StateNoCI, URL: "https://github.com/o/r/pull/1"}, false)
	if !strings.Contains(buf.String(), "⚪ o/r#1") || !strings.Contains(buf.String(), "No CI: no checks or statuses") {
		t.Errorf("output:\n%s", buf.String())
	}
}

func TestCISummary_NoCI(t *testing.T) {
	got := ciSummary(&waitResult{Owner: "o", Repo: "r", Number: 1, State: github.StateNoCI})
	if !strings.HasPrefix(got, "o/r#1 has no CI") {
		t.Errorf("summary = %q", got)
	}
}

func TestWaitCmd_NoCIExitCode(t *testing.T) {
	updated := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	setupWaitCommand(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/1"):
			fmt.Fprintf(w, `{"number":1,"title":"Stacked","state":"open","updated_at":%q,"head":{"sha":"abc1234"}}`, updated)
		case strings.HasSuffix(r.URL.Path, "/status"):
			fmt.Fprint(w, `{"state":"pending","total_count":0,"statuses":[]}`)
		default:
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		}
	})

	out, err := captureStdout(func() error {
		return waitCmd.RunE(waitCmd, []string{"https://github.com/owner/repo/pull/1"})
	})
	if code := exitCode(err); code != exitWaitNoCI {
		t.Errorf("exit code = %d (err=%v), want 4", code, err)
	}
	if !strings.Contains(out, "No CI") {
		t.Errorf("stdout:\n%s", out)
	}
}
