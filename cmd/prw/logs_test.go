package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/devblac/prw/internal/github"
)

func actionsJob(id int64, name, conclusion string) github.CheckRun {
	r := run(name, "completed", conclusion)
	r.ID = id
	r.HTMLURL = fmt.Sprintf("https://github.com/o/r/actions/runs/1/job/%d", id)
	return r
}

const goTestFailureLog = "2026-10-24T10:00:00.0000000Z ##[group]Run go test ./...\n" +
	"2026-10-24T10:00:00.0000001Z go test ./...\n" +
	"2026-10-24T10:00:00.0000002Z ##[endgroup]\n" +
	"2026-10-24T10:00:05.0000000Z --- FAIL: TestParse (0.00s)\n" +
	"2026-10-24T10:00:05.0000001Z     parse_test.go:12: got 1, want 2\n" +
	"2026-10-24T10:00:05.0000002Z FAIL\n" +
	"2026-10-24T10:00:05.0000003Z ##[error]Process completed with exit code 1.\n" +
	"2026-10-24T10:00:06.0000000Z Post job cleanup.\n"

func failingClient() *scriptedClient {
	return &scriptedClient{
		steps: []pollStep{{
			pr:   openPR("abc1234def"),
			runs: []github.CheckRun{actionsJob(7, "Test", "failure"), actionsJob(8, "Build", "success")},
		}},
		logs: map[int64]string{7: goTestFailureLog},
	}
}

func TestPrintFailedLogs(t *testing.T) {
	c := failingClient()
	c.GetPullRequest(context.Background(), "o", "r", 1) // select the step
	logs, err := fetchFailedLogs(context.Background(), c, "o", "r", "abc1234def", 40)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	printFailedLogs(&buf, logs)
	want := "\n── Test · step: go test ./... ──\n" +
		"   --- FAIL: TestParse (0.00s)\n" +
		"       parse_test.go:12: got 1, want 2\n" +
		"   FAIL\n" +
		"   Error: Process completed with exit code 1.\n" +
		"   Full log: https://github.com/o/r/actions/runs/1/job/7\n"
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestMCP_GetFailedLogs(t *testing.T) {
	h := startMCP(t, staticClient(failingClient()))
	h.initialize()

	result, text := toolResultOf(t, h.call(2, "tools/call", map[string]interface{}{
		"name": "get_failed_logs", "arguments": map[string]interface{}{"pr_url": "https://github.com/owner/repo/pull/1", "max_lines": 2},
	}))
	if result["isError"] == true {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if !strings.HasPrefix(text, "1 failed check(s) on owner/repo#1 (abc1234):") ||
		!strings.Contains(text, "── Test · step: go test ./... ──\n   …\n   FAIL\n   Error: Process completed") {
		t.Errorf("text = %q", text)
	}
	if strings.Contains(text, `"failed_logs"`) {
		t.Error("the text should show the excerpts, not repeat them as JSON")
	}
	logs := result["structuredContent"].(map[string]interface{})["failed_logs"].([]interface{})
	excerpt := logs[0].(map[string]interface{})["excerpt"].(map[string]interface{})
	if len(excerpt["lines"].([]interface{})) != 2 || excerpt["truncated"] != true {
		t.Errorf("excerpt = %v, want max_lines honored", excerpt)
	}
}

func TestMCP_GetFailedLogs_NothingFailed(t *testing.T) {
	h := startMCP(t, staticClient(successClient()))
	h.initialize()

	result, text := toolResultOf(t, h.call(2, "tools/call", map[string]interface{}{
		"name": "get_failed_logs", "arguments": map[string]string{"pr_url": "https://github.com/owner/repo/pull/1"},
	}))
	if result["isError"] == true || !strings.HasPrefix(text, "No failed checks on the head commit of owner/repo#1") {
		t.Errorf("text = %q", text)
	}
	if logs := result["structuredContent"].(map[string]interface{})["failed_logs"].([]interface{}); len(logs) != 0 {
		t.Errorf("failed_logs = %v", logs)
	}
}

// logsHandler serves a PR whose "Test" job failed, and that job's log.
func logsHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/pulls/1"):
		fmt.Fprint(w, `{"number":1,"title":"Fix parser","state":"open","head":{"sha":"abc1234"}}`)
	case strings.HasSuffix(r.URL.Path, "/status"):
		fmt.Fprint(w, `{"state":"pending","total_count":0,"statuses":[]}`)
	case strings.HasSuffix(r.URL.Path, "/actions/jobs/7/logs"):
		fmt.Fprint(w, goTestFailureLog)
	default:
		fmt.Fprint(w, `{"total_count":1,"check_runs":[{"id":7,"name":"Test","status":"completed","conclusion":"failure",`+
			`"html_url":"https://github.com/owner/repo/actions/runs/1/job/7"}]}`)
	}
}

func TestWaitCmd_LogsOnFailure(t *testing.T) {
	setupWaitCommand(t, logsHandler)
	waitLogs, waitLogLines, waitJSON = true, 40, false
	t.Cleanup(func() { waitLogs = false })

	out, err := captureStdout(func() error {
		return waitCmd.RunE(waitCmd, []string{"https://github.com/owner/repo/pull/1"})
	})
	if code := exitCode(err); code != exitWaitFailed {
		t.Errorf("exit code = %d (err=%v)", code, err)
	}
	if !strings.Contains(out, "── Test · step: go test ./... ──") || !strings.Contains(out, "parse_test.go:12: got 1, want 2") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestWaitCmd_LogsInJSON(t *testing.T) {
	setupWaitCommand(t, logsHandler)
	waitLogs, waitLogLines, waitJSON = true, 40, true
	t.Cleanup(func() { waitLogs, waitJSON = false, false })

	out, _ := captureStdout(func() error {
		return waitCmd.RunE(waitCmd, []string{"https://github.com/owner/repo/pull/1"})
	})
	var result waitResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if len(result.FailedLogs) != 1 || result.FailedLogs[0].Excerpt == nil || result.FailedLogs[0].Excerpt.Step != "go test ./..." {
		t.Errorf("failed_logs = %+v", result.FailedLogs)
	}
}

func TestChecksCmd_Logs(t *testing.T) {
	setupWaitCommand(t, logsHandler)
	checksLogs, checksLogLines, checksJSON = true, 40, false
	t.Cleanup(func() { checksLogs = false })

	out, _ := captureStdout(func() error {
		return checksCmd.RunE(checksCmd, []string{"https://github.com/owner/repo/pull/1"})
	})
	if !strings.Contains(out, "❌ fail  Test") || !strings.Contains(out, "   --- FAIL: TestParse (0.00s)") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestWaitCmd_NoLogsWithoutTheFlag(t *testing.T) {
	fetched := false
	setupWaitCommand(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/logs") {
			fetched = true
		}
		logsHandler(w, r)
	})
	waitLogs, waitJSON = false, false

	captureStdout(func() error { return waitCmd.RunE(waitCmd, []string{"https://github.com/owner/repo/pull/1"}) })
	if fetched {
		t.Error("logs must only be downloaded with --logs")
	}
}

func TestFetchFailedLogs_ListsFailedCommitStatuses(t *testing.T) {
	c := &scriptedClient{steps: []pollStep{{
		pr: openPR("sha"),
		status: &github.CombinedStatus{State: "failure", TotalCount: 2, Statuses: []github.CommitStatus{
			{Context: "Release milestone", State: "failure"},
			{Context: "ci/jenkins", State: "success", TargetURL: "https://jenkins/1"},
		}},
	}}}
	c.GetPullRequest(context.Background(), "o", "r", 1)
	logs, err := fetchFailedLogs(context.Background(), c, "o", "r", "sha", 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Name != "Release milestone" || !strings.Contains(logs[0].Error, "another CI system") {
		t.Errorf("logs = %+v", logs)
	}
}
