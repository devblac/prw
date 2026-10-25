package main

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/devblac/prw/internal/github"
)

func TestParseRepoArg(t *testing.T) {
	for arg, want := range map[string]string{
		"owner/repo":                             "owner/repo",
		"devblac/prw.go":                         "devblac/prw.go",
		"https://github.com/owner/repo":          "owner/repo",
		"https://github.com/owner/repo/pull/123": "owner/repo",
		"github.com/owner/repo/actions/runs/1":   "owner/repo",
		"https://github.com/owner/repo#readme":   "owner/repo",
	} {
		owner, repo, err := parseRepoArg(arg)
		if err != nil || owner+"/"+repo != want {
			t.Errorf("parseRepoArg(%q) = %s/%s, %v; want %s", arg, owner, repo, err, want)
		}
	}
	for _, bad := range []string{"", "owner", "https://gitlab.com/o/r", "a/b/c"} {
		if _, _, err := parseRepoArg(bad); err == nil {
			t.Errorf("parseRepoArg(%q) should fail", bad)
		}
	}
}

func flakyReport(flaky ...github.FlakyCheck) *github.FlakyReport {
	return &github.FlakyReport{Owner: "o", Repo: "r", Scanned: 300, Reruns: 4, Since: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), Flaky: flaky}
}

func TestPrintFlaky(t *testing.T) {
	var buf bytes.Buffer
	printFlaky(&buf, flakyReport(github.FlakyCheck{Workflow: "CI", Job: "Unit Tests", Count: 3,
		LastSeen: time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC), FailedJobURL: "https://github.com/o/r/actions/runs/1/job/2"}))
	out := buf.String()
	for _, want := range []string{"o/r: scanned 300 workflow run(s) since 2026-10-", "4 of them re-run", "FLAKED", "3x", "Unit Tests", "https://github.com/o/r/actions/runs/1/job/2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestPrintFlaky_NothingToCompare(t *testing.T) {
	var buf bytes.Buffer
	r := flakyReport()
	r.Reruns = 0
	printFlaky(&buf, r)
	if !strings.Contains(buf.String(), "No run was re-run") {
		t.Errorf("output:\n%s", buf.String())
	}
	buf.Reset()
	printFlaky(&buf, flakyReport())
	if !strings.Contains(buf.String(), "No flaky checks") {
		t.Errorf("output:\n%s", buf.String())
	}
}

func TestFlakyCmd(t *testing.T) {
	setupWaitCommand(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/actions/runs"):
			if r.URL.Query().Get("page") != "1" {
				fmt.Fprint(w, `{"workflow_runs":[]}`)
				return
			}
			fmt.Fprint(w, `{"workflow_runs":[{"id":9,"name":"CI","run_attempt":2,"created_at":"2026-10-24T10:00:00Z","html_url":"https://github.com/owner/repo/actions/runs/9"}]}`)
		case strings.HasSuffix(r.URL.Path, "/runs/9/attempts/1/jobs"):
			fmt.Fprint(w, `{"jobs":[{"name":"Test","conclusion":"failure","html_url":"https://github.com/owner/repo/actions/runs/9/job/1","completed_at":"2026-10-24T10:05:00Z"}]}`)
		case strings.HasSuffix(r.URL.Path, "/runs/9/attempts/2/jobs"):
			fmt.Fprint(w, `{"jobs":[{"name":"Test","conclusion":"success"}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	flakyRuns, flakyJSON = 300, false

	out, err := captureStdout(func() error {
		return flakyCmd.RunE(flakyCmd, []string{"https://github.com/owner/repo/pull/5"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1x") || !strings.Contains(out, "Test") || !strings.Contains(out, "runs/9/job/1") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestMCP_GetFlakyChecks(t *testing.T) {
	c := successClient()
	c.workflowRuns = []github.WorkflowRun{{ID: 9, Name: "CI", RunAttempt: 2}}
	c.attemptJobs = map[string][]github.Job{
		"9/1": {{Name: "Test", Conclusion: "failure", HTMLURL: "https://github.com/o/r/actions/runs/9/job/1"}},
		"9/2": {{Name: "Test", Conclusion: "success"}},
	}
	h := startMCP(t, staticClient(c))
	h.initialize()

	result, text := toolResultOf(t, h.call(2, "tools/call", map[string]interface{}{
		"name": "get_flaky_checks", "arguments": map[string]interface{}{"repo": "owner/repo"},
	}))
	if result["isError"] == true {
		t.Fatalf("tool error: %s", text)
	}
	if !strings.HasPrefix(text, "owner/repo: 1 recent workflow run(s) scanned, 1 re-run. Flaky: CI / Test (1x).") {
		t.Errorf("text = %q", strings.SplitN(text, "\n", 2)[0])
	}
	flaky := result["structuredContent"].(map[string]interface{})["flaky"].([]interface{})
	if len(flaky) != 1 || flaky[0].(map[string]interface{})["failed_job_url"] != "https://github.com/o/r/actions/runs/9/job/1" {
		t.Errorf("flaky = %v", flaky)
	}

	result, text = toolResultOf(t, h.call(3, "tools/call", map[string]interface{}{
		"name": "get_flaky_checks", "arguments": map[string]interface{}{"repo": "not a repo"},
	}))
	if result["isError"] != true {
		t.Errorf("an invalid repo must be a tool error: %s", text)
	}
}
