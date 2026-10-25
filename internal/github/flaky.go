package github

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	workflowRunsPerPage = 100
	// MaxFlakyRuns bounds how many recent workflow runs FindFlaky scans.
	MaxFlakyRuns = 1000
	// DefaultFlakyRuns is how many recent workflow runs are scanned by default.
	DefaultFlakyRuns   = 300
	attemptJobsPerPage = 100
	maxAttemptJobPages = 5
)

// WorkflowRun is one run of a GitHub Actions workflow.
type WorkflowRun struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"` // the workflow's name
	HeadSHA    string    `json:"head_sha"`
	RunAttempt int       `json:"run_attempt"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	HTMLURL    string    `json:"html_url"`
	CreatedAt  time.Time `json:"created_at"`
}

// Job is one job of one attempt of a workflow run.
type Job struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	HTMLURL     string    `json:"html_url"`
	CompletedAt time.Time `json:"completed_at"`
}

// flakyConcurrency is how many requests FindFlaky has in flight at once. On large
// repositories GitHub takes seconds per runs/jobs request.
const flakyConcurrency = 4

// ListWorkflowRuns returns up to max of the repository's most recent workflow runs,
// newest first. Pages are fetched concurrently.
func (c *Client) ListWorkflowRuns(ctx context.Context, owner, repo string, max int) ([]WorkflowRun, error) {
	pages := (max + workflowRunsPerPage - 1) / workflowRunsPerPage
	results := make([][]WorkflowRun, pages)
	err := forEachConcurrently(ctx, pages, func(i int) error {
		path := fmt.Sprintf("/repos/%s/%s/actions/runs?per_page=%d&page=%d&exclude_pull_requests=true",
			owner, repo, workflowRunsPerPage, i+1)
		var resp struct {
			WorkflowRuns []WorkflowRun `json:"workflow_runs"`
		}
		if err := c.getJSON(ctx, path, &resp); err != nil {
			return err
		}
		results[i] = resp.WorkflowRuns
		return nil
	})
	if err != nil {
		return nil, err
	}
	var runs []WorkflowRun
	for _, page := range results {
		runs = append(runs, page...)
		if len(page) < workflowRunsPerPage {
			break // the last page; later ones are empty
		}
	}
	if len(runs) > max {
		runs = runs[:max]
	}
	return runs, nil
}

// forEachConcurrently calls fn(0..n-1) with at most flakyConcurrency in flight and
// returns the first error (by index).
func forEachConcurrently(ctx context.Context, n int, fn func(i int) error) error {
	errs := make([]error, n)
	sem := make(chan struct{}, flakyConcurrency)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			errs[i] = fn(i)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// GetAttemptJobs returns the jobs of one attempt of a workflow run.
func (c *Client) GetAttemptJobs(ctx context.Context, owner, repo string, runID int64, attempt int) ([]Job, error) {
	var jobs []Job
	for page := 1; page <= maxAttemptJobPages; page++ {
		path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/attempts/%d/jobs?per_page=%d&page=%d",
			owner, repo, runID, attempt, attemptJobsPerPage, page)
		var resp struct {
			Jobs []Job `json:"jobs"`
		}
		if err := c.getJSON(ctx, path, &resp); err != nil {
			return nil, err
		}
		jobs = append(jobs, resp.Jobs...)
		if len(resp.Jobs) < attemptJobsPerPage {
			break
		}
	}
	return jobs, nil
}

// FlakyFetcher is what FindFlaky needs from the GitHub client.
type FlakyFetcher interface {
	ListWorkflowRuns(ctx context.Context, owner, repo string, max int) ([]WorkflowRun, error)
	GetAttemptJobs(ctx context.Context, owner, repo string, runID int64, attempt int) ([]Job, error)
}

// FlakyCheck is a job that failed and then passed when re-run on the same commit.
type FlakyCheck struct {
	Workflow string `json:"workflow"`
	Job      string `json:"job"`
	// Count is how many re-run workflow runs it flaked in.
	Count    int       `json:"count"`
	LastSeen time.Time `json:"last_seen"`
	// FailedJobURL is the most recent failed attempt, to look at its log.
	FailedJobURL string `json:"failed_job_url"`
}

// FlakyReport is the result of FindFlaky.
type FlakyReport struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	// Scanned is how many workflow runs were looked at; Since is the oldest one.
	Scanned int       `json:"scanned_runs"`
	Since   time.Time `json:"since,omitempty"`
	// Reruns is how many of them were re-run (only those can show flakiness).
	Reruns int          `json:"rerun_runs"`
	Flaky  []FlakyCheck `json:"flaky"`
	// Warnings are runs whose jobs couldn't be fetched.
	Warnings []string `json:"warnings,omitempty"`
}

// FindFlaky scans the repository's last maxRuns workflow runs for jobs that failed
// and then passed in a later attempt of the same run: the same commit and workflow,
// so the failure wasn't caused by the code. Jobs cancelled because another job
// failed first are not counted. Results are grouped by workflow and job name, most
// frequent first.
func FindFlaky(ctx context.Context, c FlakyFetcher, owner, repo string, maxRuns int) (*FlakyReport, error) {
	if maxRuns <= 0 {
		maxRuns = DefaultFlakyRuns
	}
	if maxRuns > MaxFlakyRuns {
		maxRuns = MaxFlakyRuns
	}
	runs, err := c.ListWorkflowRuns(ctx, owner, repo, maxRuns)
	if err != nil {
		return nil, fmt.Errorf("failed to list workflow runs: %w", err)
	}
	report := &FlakyReport{Owner: owner, Repo: repo, Scanned: len(runs), Flaky: []FlakyCheck{}}
	if len(runs) > 0 {
		report.Since = runs[len(runs)-1].CreatedAt
	}

	// Fetch the jobs of every attempt of every re-run run, concurrently.
	type fetch struct {
		run     int // index into reruns
		attempt int
	}
	var reruns []WorkflowRun
	var fetches []fetch
	for _, run := range runs {
		if run.RunAttempt < 2 {
			continue
		}
		for a := 1; a <= run.RunAttempt; a++ {
			fetches = append(fetches, fetch{len(reruns), a})
		}
		reruns = append(reruns, run)
	}
	report.Reruns = len(reruns)
	jobs := make([][][]Job, len(reruns)) // run → attempt → jobs
	failed := make([]error, len(reruns))
	for i, run := range reruns {
		jobs[i] = make([][]Job, run.RunAttempt)
	}
	var mu sync.Mutex
	_ = forEachConcurrently(ctx, len(fetches), func(i int) error {
		f := fetches[i]
		got, err := c.GetAttemptJobs(ctx, owner, repo, reruns[f.run].ID, f.attempt)
		mu.Lock()
		defer mu.Unlock()
		if err != nil && failed[f.run] == nil {
			failed[f.run] = fmt.Errorf("attempt %d: %w", f.attempt, err)
		}
		jobs[f.run][f.attempt-1] = got
		return nil // a failed run is reported as a warning, not an error
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	type key struct{ workflow, job string }
	found := map[key]*FlakyCheck{}
	for i, run := range reruns {
		if failed[i] != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("run %s %v", run.HTMLURL, failed[i]))
			continue
		}
		for _, f := range flakyJobs(jobs[i]) {
			k := key{run.Name, f.Name}
			entry := found[k]
			if entry == nil {
				entry = &FlakyCheck{Workflow: run.Name, Job: f.Name}
				found[k] = entry
			}
			entry.Count++
			seen := f.CompletedAt
			if seen.IsZero() {
				seen = run.CreatedAt
			}
			if entry.FailedJobURL == "" || seen.After(entry.LastSeen) {
				entry.LastSeen, entry.FailedJobURL = seen, f.HTMLURL
			}
		}
	}

	for _, f := range found {
		report.Flaky = append(report.Flaky, *f)
	}
	sort.Slice(report.Flaky, func(i, j int) bool {
		a, b := report.Flaky[i], report.Flaky[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.LastSeen.After(b.LastSeen)
	})
	return report, nil
}

// flakyJobs returns, for one workflow run, the failed jobs (the latest failed
// attempt of each) whose name passed in a later attempt.
func flakyJobs(attempts [][]Job) []Job {
	var flaky []Job
	seen := map[string]bool{}
	for a := len(attempts) - 2; a >= 0; a-- { // newest failure first
		for _, job := range attempts[a] {
			if seen[job.Name] || !jobFailed(job) {
				continue
			}
			for _, later := range attempts[a+1:] {
				if passed(later, job.Name) {
					flaky = append(flaky, job)
					seen[job.Name] = true
					break
				}
			}
		}
	}
	return flaky
}

func jobFailed(j Job) bool {
	switch strings.ToLower(j.Conclusion) {
	case "failure", "timed_out":
		return true
	}
	return false
}

func passed(jobs []Job, name string) bool {
	for _, j := range jobs {
		if j.Name == name && strings.EqualFold(j.Conclusion, "success") {
			return true
		}
	}
	return false
}
