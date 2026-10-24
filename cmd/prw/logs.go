package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/devblac/prw/internal/github"
)

// maxMCPLogLines caps the log lines per failed job returned to agents.
const maxMCPLogLines = 200

// fetchFailedLogs gets log excerpts of the failed GitHub Actions jobs of a commit.
// Failed commit statuses (external CI) are listed too, with their link, since they
// have no log in the API.
func fetchFailedLogs(ctx context.Context, client ciClient, owner, repo, sha string, maxLines int) ([]github.FailedJobLog, error) {
	fetcher, ok := client.(github.LogFetcher)
	if !ok {
		return nil, fmt.Errorf("this client can't download job logs")
	}
	runs, err := client.GetCheckRuns(ctx, owner, repo, sha)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch check runs: %w", err)
	}
	logs, err := github.FailedJobLogs(ctx, fetcher, owner, repo, runs, maxLines)
	if err != nil {
		return nil, err
	}
	status, err := client.GetCombinedStatus(ctx, owner, repo, sha)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch commit status: %w", err)
	}
	for _, s := range status.Statuses {
		if state := github.NormalizeState(s.State); state == "failure" || state == "error" {
			logs = append(logs, github.FailedJobLog{Name: s.Context, URL: s.TargetURL,
				Error: "commit status from another CI system; its log isn't available through the API"})
		}
	}
	return logs, nil
}

// printFailedLogs prints each failed job's excerpt below a header naming the job and
// its failed step.
func printFailedLogs(w io.Writer, logs []github.FailedJobLog) {
	for _, l := range logs {
		header := "── " + l.Name
		if l.Excerpt != nil && l.Excerpt.Step != "" {
			header += " · step: " + l.Excerpt.Step
		}
		fmt.Fprintf(w, "\n%s ──\n", header)
		if l.Excerpt == nil {
			fmt.Fprintf(w, "   (no log: %s)\n", l.Error)
		} else {
			if l.Excerpt.Truncated {
				fmt.Fprintln(w, "   …")
			}
			for _, line := range l.Excerpt.Lines {
				fmt.Fprintf(w, "   %s\n", line)
			}
		}
		if l.URL != "" {
			fmt.Fprintf(w, "   Full log: %s\n", l.URL)
		}
	}
}

// failedLogsText renders logs for an agent: plain text, one block per failed job.
func failedLogsText(logs []github.FailedJobLog) string {
	var b strings.Builder
	printFailedLogs(&b, logs)
	return strings.TrimLeft(b.String(), "\n")
}
