package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestExtractFailure_RealStaticcheckLog(t *testing.T) {
	// End of a real job log (devblac/go-semver-audit, Lint job): timestamps, ANSI
	// colors, step groups, the failing step's output, and post-job cleanup after it.
	data, err := os.ReadFile("testdata/staticcheck_failure.log")
	if err != nil {
		t.Fatal(err)
	}
	ex := ExtractFailure(string(data), 40)

	if ex.Step != "staticcheck ./..." {
		t.Errorf("step = %q", ex.Step)
	}
	if len(ex.Lines) != 6 || ex.Truncated {
		t.Fatalf("got %d lines (truncated=%v), want the 5 output lines + the error:\n%s", len(ex.Lines), ex.Truncated, strings.Join(ex.Lines, "\n"))
	}
	if !strings.HasPrefix(ex.Lines[0], `-: internal error in importing "cmp"`) {
		t.Errorf("first line = %q", ex.Lines[0])
	}
	if ex.Lines[5] != "Error: Process completed with exit code 1." {
		t.Errorf("last line = %q", ex.Lines[5])
	}
	for _, l := range ex.Lines {
		if strings.Contains(l, "\x1b") || strings.HasPrefix(l, "2026-") || strings.Contains(l, "Post job cleanup") {
			t.Errorf("line not cleaned up or outside the step: %q", l)
		}
	}
}

func TestExtractFailure_KeepsTheEndOfLongOutput(t *testing.T) {
	var b strings.Builder
	b.WriteString("##[group]Run go test ./...\ngo test ./...\n##[endgroup]\n")
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	b.WriteString("##[error]Process completed with exit code 1.\n")

	ex := ExtractFailure(b.String(), 10)
	if len(ex.Lines) != 10 || !ex.Truncated || ex.Lines[0] != "line 92" || ex.Step != "go test ./..." {
		t.Errorf("excerpt = %+v", ex)
	}
}

func TestExtractFailure_WithoutErrorMarker(t *testing.T) {
	ex := ExtractFailure("a\nb\nc\n\n", 2)
	if strings.Join(ex.Lines, ",") != "b,c" || !ex.Truncated {
		t.Errorf("excerpt = %+v, want the last 2 non-empty lines", ex)
	}
}

func TestExtractFailure_DropsRunnerMarkup(t *testing.T) {
	// From a composite action failing in cloudposse/atmos.
	log := "##[group]Run ./.github/actions/setup\n##[endgroup]\n" +
		"##[start-action display=Download artifact;id=__self_2.__self]\n" +
		"##[debug]Evaluating condition\n" +
		"Getting action download info\n" +
		"##[warning]Back off 27s before retry.\n" +
		"##[error]Resource temporarily unavailable\n" +
		"##[end-action id=__self_2.__self;outcome=failure]\n"
	ex := ExtractFailure(log, 40)
	want := "Getting action download info|Warning: Back off 27s before retry.|Error: Resource temporarily unavailable"
	if strings.Join(ex.Lines, "|") != want || ex.Step != "./.github/actions/setup" {
		t.Errorf("excerpt = %+v", ex)
	}
}

func TestExtractFailure_ConsecutiveErrorsStayTogether(t *testing.T) {
	ex := ExtractFailure("##[group]Run make\nmake\n##[endgroup]\nboom\n##[error]first\n##[error]second\nPost job cleanup.\n", 40)
	if strings.Join(ex.Lines, "|") != "boom|Error: first|Error: second" {
		t.Errorf("lines = %q", ex.Lines)
	}
}

func TestReadTail(t *testing.T) {
	data := strings.Repeat("x", 100) + "\nkeep 1\nkeep 2\n"
	got, err := readTail(strings.NewReader(data), 20)
	if err != nil {
		t.Fatal(err)
	}
	if got != "keep 1\nkeep 2\n" {
		t.Errorf("tail = %q, want the last whole lines within 20 bytes", got)
	}
}

func TestIsActionsJob(t *testing.T) {
	if !IsActionsJob(CheckRun{HTMLURL: "https://github.com/o/r/actions/runs/1/job/2"}) {
		t.Error("an Actions job URL must be recognized")
	}
	if IsActionsJob(CheckRun{HTMLURL: "https://circleci.com/gh/o/r/1"}) {
		t.Error("an external check must not be")
	}
}

func TestGetJobLog_FollowsRedirectWithoutLeakingToken(t *testing.T) {
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("the token must not be sent to the log storage host")
		}
		fmt.Fprint(w, "##[group]Run go test\n##[endgroup]\nFAIL\n##[error]exit 1\n")
	}))
	defer storage.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/actions/jobs/42/logs" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected request %s (auth %q)", r.URL.Path, r.Header.Get("Authorization"))
		}
		// A different host, like GitHub's blob storage.
		http.Redirect(w, r, strings.Replace(storage.URL, "127.0.0.1", "localhost", 1)+"/log.txt", http.StatusFound)
	}))
	defer api.Close()
	c := NewClient("secret")
	c.BaseURL = api.URL

	log, err := c.GetJobLog(context.Background(), "o", "r", 42)
	if err != nil {
		t.Fatal(err)
	}
	if ex := ExtractFailure(log, 40); strings.Join(ex.Lines, "|") != "FAIL|Error: exit 1" {
		t.Errorf("excerpt = %+v", ex)
	}
}

func TestGetJobLog_ExplainsMissingPermission(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Resource not accessible by personal access token"}`)
	}))
	defer srv.Close()
	c := NewClient("t")
	c.BaseURL = srv.URL
	if _, err := c.GetJobLog(context.Background(), "o", "r", 1); err == nil || !strings.Contains(err.Error(), "Actions: read") {
		t.Errorf("err = %v", err)
	}
}

type fakeLogs struct {
	logs    map[int64]string
	fetched []int64
}

func (f *fakeLogs) GetJobLog(ctx context.Context, owner, repo string, jobID int64) (string, error) {
	f.fetched = append(f.fetched, jobID)
	if log, ok := f.logs[jobID]; ok {
		return log, nil
	}
	return "", errors.New("log not available")
}

func TestFailedJobLogs(t *testing.T) {
	job := func(id int64, name, conclusion string) CheckRun {
		r := actionsRun(id, name, "completed", conclusion)
		r.HTMLURL = fmt.Sprintf("https://github.com/o/r/actions/runs/1/job/%d", id)
		return r
	}
	external := CheckRun{ID: 9, Name: "circleci", Status: "completed", Conclusion: "failure", HTMLURL: "https://circleci.com/x"}
	runs := []CheckRun{
		job(1, "Test", "failure"),
		job(2, "Build", "success"),
		job(3, "E2E", "cancelled"),
		job(4, "Lint", "timed_out"),
		job(5, "Docs", "failure"), job(6, "Docs", "success"), // superseded failure
		external,
	}
	f := &fakeLogs{logs: map[int64]string{1: "boom\n##[error]exit 1\n"}}

	got, err := FailedJobLogs(context.Background(), f, "o", "r", runs, 40)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range got {
		names = append(names, l.Name)
	}
	if strings.Join(names, ",") != "circleci,Lint,Test" { // sorted by name, like `prw checks`
		t.Fatalf("failed jobs = %v", names)
	}
	if got[2].Excerpt == nil || got[2].Excerpt.Lines[0] != "boom" {
		t.Errorf("Test excerpt = %+v", got[2])
	}
	if got[1].Error != "log not available" || got[0].Excerpt != nil || !strings.Contains(got[0].Error, "not a GitHub Actions job") {
		t.Errorf("errors: %+v / %+v", got[1], got[0])
	}
	if fmt.Sprint(f.fetched) != "[4 1]" {
		t.Errorf("fetched logs of %v, want only the failed Actions jobs", f.fetched)
	}
}

func TestFailedJobLogs_BoundsDownloads(t *testing.T) {
	var runs []CheckRun
	for i := int64(1); i <= 8; i++ {
		r := actionsRun(i, fmt.Sprintf("Test %d", i), "completed", "failure")
		r.HTMLURL = fmt.Sprintf("https://github.com/o/r/actions/runs/1/job/%d", i)
		runs = append(runs, r)
	}
	f := &fakeLogs{}
	got, _ := FailedJobLogs(context.Background(), f, "o", "r", runs, 40)
	if len(got) != 8 || len(f.fetched) != maxLogFetches || !strings.Contains(got[7].Error, "skipped") {
		t.Errorf("got %d entries, fetched %d logs", len(got), len(f.fetched))
	}
}

func TestReadTail_LongStream(t *testing.T) {
	// Larger than the internal buffer, so it is compacted while reading.
	data := strings.Repeat("noise line\n", 50000) + "last line\n"
	got, err := readTail(strings.NewReader(data), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 1000 || !strings.HasPrefix(got, "noise line\n") || !strings.HasSuffix(got, "last line\n") {
		t.Errorf("tail has %d bytes, starts %q", len(got), got[:20])
	}
}
