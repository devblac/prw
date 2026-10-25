package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeFlaky struct {
	runs []WorkflowRun
	jobs map[string][]Job // "runID/attempt"
	errs map[string]error
	max  int
}

func (f *fakeFlaky) ListWorkflowRuns(ctx context.Context, owner, repo string, max int) ([]WorkflowRun, error) {
	f.max = max
	return f.runs, nil
}

func (f *fakeFlaky) GetAttemptJobs(ctx context.Context, owner, repo string, runID int64, attempt int) ([]Job, error) {
	k := fmt.Sprintf("%d/%d", runID, attempt)
	if err := f.errs[k]; err != nil {
		return nil, err
	}
	return f.jobs[k], nil
}

var flakyNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func wfRun(id int64, workflow string, attempts int, ago time.Duration) WorkflowRun {
	return WorkflowRun{ID: id, Name: workflow, RunAttempt: attempts, CreatedAt: flakyNow.Add(-ago),
		HTMLURL: fmt.Sprintf("https://github.com/o/r/actions/runs/%d", id)}
}

func job(name, conclusion string, ago time.Duration) Job {
	return Job{Name: name, Conclusion: conclusion, Status: "completed", CompletedAt: flakyNow.Add(-ago),
		HTMLURL: "https://github.com/o/r/actions/runs/x/job/" + strings.ReplaceAll(name, " ", "_")}
}

func TestFindFlaky(t *testing.T) {
	f := &fakeFlaky{
		runs: []WorkflowRun{
			wfRun(1, "CI", 2, time.Hour),       // Unit Tests failed, passed on re-run → flaky
			wfRun(2, "CI", 1, 2*time.Hour),     // single attempt: can't tell
			wfRun(3, "CI", 3, 3*time.Hour),     // Unit Tests flaked again (attempts 1 and 2)
			wfRun(4, "Deploy", 2, 4*time.Hour), // Release failed twice: a real failure
		},
		jobs: map[string][]Job{
			"1/1": {job("Unit Tests", "failure", 70*time.Minute), job("E2E", "cancelled", 70*time.Minute), job("Lint", "success", 70*time.Minute)},
			"1/2": {job("Unit Tests", "success", 60*time.Minute), job("E2E", "success", 60*time.Minute), job("Lint", "success", 70*time.Minute)},
			"3/1": {job("Unit Tests", "failure", 200*time.Minute)},
			"3/2": {job("Unit Tests", "timed_out", 190*time.Minute)},
			"3/3": {job("Unit Tests", "success", 180*time.Minute)},
			"4/1": {job("Release", "failure", 230*time.Minute)},
			"4/2": {job("Release", "failure", 220*time.Minute)},
		},
	}

	report, err := FindFlaky(context.Background(), f, "o", "r", 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.max != DefaultFlakyRuns || report.Scanned != 4 || report.Reruns != 3 || !report.Since.Equal(flakyNow.Add(-4*time.Hour)) {
		t.Errorf("report = %+v (max %d)", report, f.max)
	}
	if len(report.Flaky) != 1 {
		t.Fatalf("flaky = %+v, want only Unit Tests (E2E was cancelled, Release never passed)", report.Flaky)
	}
	got := report.Flaky[0]
	if got.Workflow != "CI" || got.Job != "Unit Tests" || got.Count != 2 || !got.LastSeen.Equal(flakyNow.Add(-70*time.Minute)) {
		t.Errorf("flaky check = %+v, want CI / Unit Tests counted once per run, last seen in run 1", got)
	}
}

func TestFindFlaky_OrdersByCountThenRecency(t *testing.T) {
	f := &fakeFlaky{
		runs: []WorkflowRun{wfRun(1, "CI", 2, time.Hour), wfRun(2, "CI", 2, 2*time.Hour), wfRun(3, "CI", 2, 3*time.Hour)},
		jobs: map[string][]Job{
			"1/1": {job("B", "failure", time.Hour)}, "1/2": {job("B", "success", time.Hour)},
			"2/1": {job("A", "failure", 2*time.Hour)}, "2/2": {job("A", "success", 2*time.Hour)},
			"3/1": {job("A", "failure", 3*time.Hour), job("C", "failure", 3*time.Hour)},
			"3/2": {job("A", "success", 3*time.Hour), job("C", "success", 3*time.Hour)},
		},
	}
	report, _ := FindFlaky(context.Background(), f, "o", "r", 100)
	var order []string
	for _, c := range report.Flaky {
		order = append(order, fmt.Sprintf("%s:%d", c.Job, c.Count))
	}
	if strings.Join(order, " ") != "A:2 B:1 C:1" {
		t.Errorf("order = %v", order)
	}
}

func TestFindFlaky_SkipsRunsWhoseJobsFail(t *testing.T) {
	f := &fakeFlaky{
		runs: []WorkflowRun{wfRun(1, "CI", 2, time.Hour)},
		errs: map[string]error{"1/1": errors.New("boom")},
	}
	report, err := FindFlaky(context.Background(), f, "o", "r", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Flaky) != 0 || len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "boom") {
		t.Errorf("report = %+v", report)
	}
}

func TestFindFlaky_CapsRuns(t *testing.T) {
	f := &fakeFlaky{}
	FindFlaky(context.Background(), f, "o", "r", 5000)
	if f.max != MaxFlakyRuns {
		t.Errorf("max = %d, want %d", f.max, MaxFlakyRuns)
	}
}

func TestListWorkflowRuns_PagesInOrderAndStopsAtTheLastPage(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		if r.URL.Query().Get("exclude_pull_requests") != "true" {
			t.Error("runs must be listed without the pull_requests payload")
		}
		var page int
		fmt.Sscan(r.URL.Query().Get("page"), &page)
		count := map[int]int{1: 100, 2: 100, 3: 30}[page] // pages 4+ are empty
		parts := make([]string, count)
		for i := range parts {
			parts[i] = fmt.Sprintf(`{"id":%d,"run_attempt":1}`, (page-1)*100+i+1)
		}
		fmt.Fprintf(w, `{"workflow_runs":[%s]}`, strings.Join(parts, ","))
	}))
	defer srv.Close()
	c := NewClient("t")
	c.BaseURL = srv.URL

	runs, err := c.ListWorkflowRuns(context.Background(), "o", "r", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 230 || runs[0].ID != 1 || runs[100].ID != 101 || runs[229].ID != 230 {
		t.Errorf("got %d runs (first %d), want 230 in page order", len(runs), runs[0].ID)
	}
	if n := atomic.LoadInt32(&requests); n != 5 {
		t.Errorf("%d requests, want 5 pages for max 500", n)
	}

	if runs, _ := c.ListWorkflowRuns(context.Background(), "o", "r", 150); len(runs) != 150 {
		t.Errorf("max 150: got %d runs", len(runs))
	}
}
