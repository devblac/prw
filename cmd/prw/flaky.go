package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
)

var (
	flakyRuns int
	flakyJSON bool
)

func init() {
	rootCmd.AddCommand(flakyCmd)
	flakyCmd.Flags().IntVar(&flakyRuns, "runs", github.DefaultFlakyRuns, fmt.Sprintf("how many recent workflow runs to scan (max %d)", github.MaxFlakyRuns))
	flakyCmd.Flags().BoolVar(&flakyJSON, "json", false, "print the result as JSON")
}

var flakyCmd = &cobra.Command{
	Use:   "flaky <owner/repo | PR_URL>",
	Short: "Find flaky checks: jobs that failed and then passed on re-run",
	Long: `Scan a repository's recent GitHub Actions workflow runs for flaky jobs: jobs that
failed and then passed when the run was re-run, on the same commit. The code
didn't change between the attempts, so the failure wasn't caused by it.

Only re-run workflow runs can show this. Jobs cancelled because another job failed
first are not counted. Results are grouped by workflow and job, most frequent first,
with a link to the latest failed attempt.`,
	Example: `  prw flaky owner/repo
  prw flaky https://github.com/owner/repo/pull/123 --runs 1000
  prw flaky owner/repo --json`,
	SilenceUsage: true,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return waitErrorf(exitWaitError, "expected a repository (owner/repo) or a PR URL, got %d argument(s)", len(args))
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		owner, repo, err := parseRepoArg(args[0])
		if err != nil {
			return waitErrorf(exitWaitError, "%v", err)
		}
		cfg, err := config.Load()
		if err != nil {
			return waitErrorf(exitWaitError, "failed to load config: %v", err)
		}
		token := cfg.GetToken()
		if token == "" {
			return &exitError{code: exitWaitError, err: errMissingToken}
		}

		fmt.Fprintf(os.Stderr, "Scanning the last %d workflow runs of %s/%s...\n", flakyRuns, owner, repo)
		report, err := github.FindFlaky(context.Background(), newGitHubClient(token), owner, repo, flakyRuns)
		if err != nil {
			return &exitError{code: exitWaitError, err: err}
		}
		for _, w := range report.Warnings {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
		}
		if flakyJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(report); err != nil {
				return &exitError{code: exitWaitError, err: err}
			}
			return nil
		}
		printFlaky(os.Stdout, report)
		return nil
	},
}

var (
	repoSlug = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)$`)
	repoURL  = regexp.MustCompile(`^(?:https?://)?github\.com/([^/]+)/([^/#?]+)`)
)

// parseRepoArg accepts "owner/repo", a repository URL or a PR URL.
func parseRepoArg(arg string) (owner, repo string, err error) {
	if strings.Contains(strings.ToLower(arg), "bitbucket.org") {
		return "", "", fmt.Errorf("flaky checks are only found for GitHub Actions so far, not Bitbucket")
	}
	if m := repoSlug.FindStringSubmatch(arg); m != nil {
		return m[1], m[2], nil
	}
	if m := repoURL.FindStringSubmatch(arg); m != nil {
		return m[1], m[2], nil
	}
	return "", "", fmt.Errorf("invalid repository %q: use owner/repo or a GitHub URL", arg)
}

func printFlaky(w io.Writer, r *github.FlakyReport) {
	scope := fmt.Sprintf("%d workflow run(s)", r.Scanned)
	if !r.Since.IsZero() {
		scope += " since " + r.Since.Local().Format("2006-01-02")
	}
	fmt.Fprintf(w, "%s/%s: scanned %s, %d of them re-run.\n", r.Owner, r.Repo, scope, r.Reruns)

	switch {
	case r.Reruns == 0:
		fmt.Fprintln(w, "\nNo run was re-run, so there's nothing to compare: flakiness shows up when a failed\njob passes after re-running it on the same commit. Try a larger --runs.")
		return
	case len(r.Flaky) == 0:
		fmt.Fprintln(w, "\nNo flaky checks: no job failed and then passed in a re-run.")
		return
	}

	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "FLAKED\tWORKFLOW\tJOB\tLAST SEEN\tLATEST FAILED ATTEMPT")
	for _, f := range r.Flaky {
		fmt.Fprintf(tw, "%dx\t%s\t%s\t%s\t%s\n", f.Count, f.Workflow, f.Job, f.LastSeen.Local().Format("2006-01-02 15:04"), f.FailedJobURL)
	}
	tw.Flush()
}
