package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
)

// exitChecksPending is `prw checks`' exit code while CI is still running; the other
// codes are the ones of `prw wait`. 8 is what `gh pr checks` uses for pending.
const exitChecksPending = 8

// Buckets of individual checks, in display order.
const (
	checkFail     = "fail"
	checkCancel   = "cancel"
	checkPending  = "pending"
	checkPass     = "pass"
	checkNeutral  = "neutral"
	checkSkipping = "skipping"
)

var checksJSON bool

func init() {
	rootCmd.AddCommand(checksCmd)
	checksCmd.Flags().BoolVar(&checksJSON, "json", false, "print the checks as JSON")
}

var checksCmd = &cobra.Command{
	Use:   "checks <PR_URL>",
	Short: "Show every check of a PR with its result, duration and link",
	Long: `Show the individual checks (GitHub Actions jobs and other check runs, plus legacy
commit statuses) of a pull request's head commit: failures first, then running
ones, then the rest. The PR does not need to be on the watch list.

Exit codes:
  0  all checks passed
  1  a check failed
  3  error (invalid URL, API error, PR closed without merging)
  4  the PR has no CI
  8  checks are still running`,
	Example: `  prw checks https://github.com/owner/repo/pull/123
  prw checks https://github.com/owner/repo/pull/123 --json`,
	SilenceUsage: true,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return waitErrorf(exitWaitError, "expected exactly one PR URL, got %d argument(s)", len(args))
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		owner, repo, number, err := github.ParsePRURL(args[0])
		if err != nil {
			return waitErrorf(exitWaitError, "invalid PR URL: %v", err)
		}
		cfg, err := config.Load()
		if err != nil {
			return waitErrorf(exitWaitError, "failed to load config: %v", err)
		}
		token := cfg.GetToken()
		if token == "" {
			return &exitError{code: exitWaitError, err: errMissingToken}
		}

		report, err := fetchChecks(context.Background(), newGitHubClient(token), owner, repo, number, time.Now())
		if err != nil {
			return &exitError{code: exitWaitError, err: err}
		}
		if checksJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(report); err != nil {
				return &exitError{code: exitWaitError, err: err}
			}
		} else {
			printChecks(os.Stdout, report, time.Now())
		}
		return report.exitErr()
	},
}

// checkEntry is one check run or commit status; it is also the --json output.
type checkEntry struct {
	Name        string     `json:"name"`
	Bucket      string     `json:"bucket"`               // fail, cancel, pending, pass, neutral, skipping
	State       string     `json:"state"`                // GitHub's conclusion, or status while not completed
	Kind        string     `json:"kind"`                 // check_run or status
	URL         string     `json:"url,omitempty"`        // job / details page
	StartedAt   *time.Time `json:"started_at,omitempty"` // check runs only
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// checksReport is the output of `prw checks`.
type checksReport struct {
	Owner      string       `json:"owner"`
	Repo       string       `json:"repo"`
	Number     int          `json:"number"`
	Title      string       `json:"title,omitempty"`
	URL        string       `json:"url"`
	SHA        string       `json:"sha"`
	State      string       `json:"state"` // overall, as in `prw wait`
	MergeState string       `json:"merge_state,omitempty"`
	Checks     []checkEntry `json:"checks"`
}

// fetchChecks gets the head commit's check runs and statuses. Superseded runs of the
// same check are left out, as they don't count towards the result.
func fetchChecks(ctx context.Context, client ciClient, owner, repo string, number int, now time.Time) (*checksReport, error) {
	pr, err := client.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PR: %w", err)
	}
	sha := pr.Head.SHA
	status, err := client.GetCombinedStatus(ctx, owner, repo, sha)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch commit status: %w", err)
	}
	runs, err := client.GetCheckRuns(ctx, owner, repo, sha)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch check runs: %w", err)
	}

	ci := github.SummarizeCI(status, runs)
	github.ApplyNoCI(ci, pr.UpdatedAt, now)
	report := &checksReport{
		Owner: owner, Repo: repo, Number: number, Title: pr.Title, SHA: sha,
		URL: github.FormatPRURL(owner, repo, number), State: ci.State, MergeState: github.MergeStatus(pr),
		Checks: []checkEntry{},
	}
	if pr.IsClosed() {
		report.State = "closed"
		if pr.Merged {
			report.State = "merged"
		}
	}

	for _, run := range github.LatestCheckRuns(runs) {
		entry := checkEntry{Name: run.Name, Kind: "check_run", URL: run.HTMLURL, State: strings.ToLower(run.Conclusion)}
		if !strings.EqualFold(run.Status, "completed") {
			entry.State = strings.ToLower(run.Status)
		}
		entry.Bucket = checkRunBucket(run)
		if !run.StartedAt.IsZero() {
			t := run.StartedAt
			entry.StartedAt = &t
		}
		if !run.CompletedAt.IsZero() {
			t := run.CompletedAt
			entry.CompletedAt = &t
		}
		report.Checks = append(report.Checks, entry)
	}
	for _, s := range status.Statuses {
		state := github.NormalizeState(s.State)
		bucket := checkPending
		switch state {
		case "success":
			bucket = checkPass
		case "failure", "error":
			bucket = checkFail
		}
		report.Checks = append(report.Checks, checkEntry{Name: s.Context, Kind: "status", URL: s.TargetURL, State: state, Bucket: bucket})
	}

	order := map[string]int{checkFail: 0, checkCancel: 1, checkPending: 2, checkPass: 3, checkNeutral: 4, checkSkipping: 5}
	sort.SliceStable(report.Checks, func(i, j int) bool {
		a, b := report.Checks[i], report.Checks[j]
		if order[a.Bucket] != order[b.Bucket] {
			return order[a.Bucket] < order[b.Bucket]
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return report, nil
}

// checkRunBucket classifies a check run like SummarizeCI does: everything that isn't a
// pass, neutral or skip is a failure (cancelled ones are shown apart).
func checkRunBucket(run github.CheckRun) string {
	if !strings.EqualFold(run.Status, "completed") {
		return checkPending
	}
	switch strings.ToLower(run.Conclusion) {
	case "success":
		return checkPass
	case "neutral":
		return checkNeutral
	case "skipped":
		return checkSkipping
	case "cancelled":
		return checkCancel
	default: // failure, timed_out, action_required, startup_failure, stale
		return checkFail
	}
}

func (r *checksReport) exitErr() error {
	pr := fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number)
	switch r.State {
	case "success", "merged":
		return nil
	case "closed":
		// Like `prw wait`: an abandoned PR must not pass `prw checks "$PR" && ...`.
		return waitErrorf(exitWaitError, "%s was closed without merging", pr)
	case "failure", "error":
		return waitErrorf(exitWaitFailed, "CI of %s finished with %s", pr, r.State)
	case github.StateNoCI:
		return waitErrorf(exitWaitNoCI, "%s has no CI", pr)
	default:
		return &exitError{code: exitChecksPending, err: fmt.Errorf("CI of %s is still running", pr)}
	}
}

var bucketIcon = map[string]string{
	checkFail: "❌", checkCancel: "🚫", checkPending: "⏳", checkPass: "✅", checkNeutral: "⚪", checkSkipping: "⏭",
}

func printChecks(w io.Writer, r *checksReport, now time.Time) {
	fmt.Fprintf(w, "%s/%s#%d", r.Owner, r.Repo, r.Number)
	if r.Title != "" {
		fmt.Fprintf(w, " — %s", r.Title)
	}
	fmt.Fprintln(w)

	counts := map[string]int{}
	for _, c := range r.Checks {
		counts[c.Bucket]++
	}
	var parts []string
	for _, b := range []string{checkFail, checkCancel, checkPending, checkPass, checkNeutral, checkSkipping} {
		if counts[b] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[b], b))
		}
	}
	summary := fmt.Sprintf("%s · %s", shortSHA(r.SHA), r.State)
	if len(parts) > 0 {
		summary += " · " + strings.Join(parts, ", ")
	}
	if r.MergeState != "" {
		summary += " · merge: " + r.MergeState
	}
	fmt.Fprintln(w, summary)

	if len(r.Checks) == 0 {
		fmt.Fprintln(w, "\nNo checks or statuses reported for this commit.")
		return
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range r.Checks {
		fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\n", bucketIcon[c.Bucket], c.Bucket, c.Name, checkDuration(c, now), c.URL)
	}
	tw.Flush()
}

// checkDuration is how long a check took, or has been running so far ("3m10s…").
func checkDuration(c checkEntry, now time.Time) string {
	if c.StartedAt == nil {
		return ""
	}
	if c.CompletedAt == nil {
		return formatCheckDuration(now.Sub(*c.StartedAt)) + "…"
	}
	return formatCheckDuration(c.CompletedAt.Sub(*c.StartedAt))
}

func formatCheckDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
