package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/devblac/prw/internal/github"
)

var checksNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func timedRun(id int64, name, status, conclusion string, started, completed time.Duration) github.CheckRun {
	r := github.CheckRun{ID: id, Name: name, Status: status, Conclusion: conclusion, HTMLURL: "https://ci/" + name}
	r.App.ID = 15368
	if started != 0 {
		r.StartedAt = checksNow.Add(-started)
	}
	if completed != 0 {
		r.CompletedAt = checksNow.Add(-completed)
	}
	return r
}

func checksFixture() *scriptedClient {
	pr := openPR("abc1234def")
	pr.MergeableState = "blocked"
	return &scriptedClient{steps: []pollStep{{
		pr: pr,
		status: &github.CombinedStatus{State: "failure", TotalCount: 1, Statuses: []github.CommitStatus{
			{Context: "ci/jenkins", State: "error", TargetURL: "https://jenkins/1"},
		}},
		runs: []github.CheckRun{
			timedRun(1, "Build", "completed", "success", 5*time.Minute, 4*time.Minute),
			timedRun(2, "Test", "completed", "failure", 5*time.Minute, time.Minute),
			timedRun(3, "Lint", "in_progress", "", 70*time.Second, 0),
			timedRun(4, "Deploy", "completed", "skipped", 0, 0),
			timedRun(5, "E2E", "completed", "cancelled", 3*time.Minute, 2*time.Minute),
			timedRun(6, "Docs", "completed", "failure", 9*time.Minute, 8*time.Minute), // superseded by 7
			timedRun(7, "Docs", "completed", "success", 2*time.Minute, time.Minute),
		},
	}}}
}

func TestFetchChecks_BucketsAndOrder(t *testing.T) {
	report, err := fetchChecks(context.Background(), checksFixture(), "o", "r", 1, checksNow)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range report.Checks {
		got = append(got, c.Bucket+":"+c.Name)
	}
	want := "fail:ci/jenkins fail:Test cancel:E2E pending:Lint pass:Build pass:Docs skipping:Deploy"
	if strings.Join(got, " ") != want {
		t.Errorf("checks = %s\nwant     %s", strings.Join(got, " "), want)
	}
	if report.State != "failure" || report.MergeState != github.MergeBlocked || report.SHA != "abc1234def" {
		t.Errorf("report = %+v", report)
	}
	if code := exitCode(report.exitErr()); code != exitWaitFailed {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestPrintChecks(t *testing.T) {
	report, err := fetchChecks(context.Background(), checksFixture(), "o", "r", 1, checksNow)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	printChecks(&buf, report, checksNow)
	out := buf.String()
	for _, want := range []string{
		"o/r#1 — Add feature",
		"abc1234 · failure · 2 fail, 1 cancel, 1 pending, 2 pass, 1 skipping · merge: blocked",
		"❌ fail", "https://jenkins/1",
		"Test", "4m00s", // 5m ago → 1m ago
		"Lint", "1m10s…", // still running
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "https://ci/Docs") != 1 {
		t.Errorf("superseded run of Docs must not be listed:\n%s", out)
	}
}

func TestChecksExitCodes(t *testing.T) {
	for state, want := range map[string]int{
		"success": 0, "merged": 0, "closed": exitWaitError, "failure": exitWaitFailed, "error": exitWaitFailed,
		github.StateNoCI: exitWaitNoCI, "pending": exitChecksPending,
	} {
		r := &checksReport{Owner: "o", Repo: "r", Number: 1, State: state}
		if got := exitCode(r.exitErr()); got != want {
			t.Errorf("%s: exit %d, want %d", state, got, want)
		}
	}
}

func TestFormatCheckDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0s", 42 * time.Second: "42s", 61 * time.Second: "1m01s", 3*time.Hour + 5*time.Minute: "3h05m", -time.Second: "0s",
	} {
		if got := formatCheckDuration(d); got != want {
			t.Errorf("formatCheckDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestChecksCmd_PendingExitCode(t *testing.T) {
	setupWaitCommand(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/1"):
			fmt.Fprint(w, `{"number":1,"title":"WIP","state":"open","mergeable_state":"unstable","head":{"sha":"abc1234"}}`)
		case strings.HasSuffix(r.URL.Path, "/status"):
			fmt.Fprint(w, `{"state":"pending","total_count":0,"statuses":[]}`)
		default:
			fmt.Fprint(w, `{"total_count":1,"check_runs":[{"id":1,"name":"Test","status":"in_progress","started_at":"2026-10-24T11:59:00Z"}]}`)
		}
	})
	checksJSON = false

	out, err := captureStdout(func() error {
		return checksCmd.RunE(checksCmd, []string{"https://github.com/owner/repo/pull/1"})
	})
	if code := exitCode(err); code != exitChecksPending {
		t.Errorf("exit code = %d (err=%v), want 8", code, err)
	}
	if !strings.Contains(out, "⏳ pending  Test") || !strings.Contains(out, "merge: unstable") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestChecksCmd_NoChecks(t *testing.T) {
	setupWaitCommand(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/1"):
			fmt.Fprint(w, `{"number":1,"state":"open","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc1234"}}`)
		case strings.HasSuffix(r.URL.Path, "/status"):
			fmt.Fprint(w, `{"state":"pending","total_count":0,"statuses":[]}`)
		default:
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		}
	})
	checksJSON = true
	t.Cleanup(func() { checksJSON = false })

	out, err := captureStdout(func() error {
		return checksCmd.RunE(checksCmd, []string{"https://github.com/owner/repo/pull/1"})
	})
	if code := exitCode(err); code != exitWaitNoCI {
		t.Errorf("exit code = %d (err=%v), want 4", code, err)
	}
	if !strings.Contains(out, `"state": "no_ci"`) || !strings.Contains(out, `"checks": []`) {
		t.Errorf("stdout:\n%s", out)
	}
}
