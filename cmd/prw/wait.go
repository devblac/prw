package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
)

// Exit codes of `prw wait`, so scripts and agents can branch on the result.
const (
	exitWaitSuccess     = 0
	exitWaitFailed      = 1
	exitWaitTimeout     = 2
	exitWaitError       = 3
	exitWaitNoCI        = 4
	exitWaitInterrupted = 130

	// maxConsecutiveWaitErrors is how many transient API errors in a row `prw wait`
	// tolerates before giving up, so one network blip doesn't abort a long wait.
	maxConsecutiveWaitErrors = 3
)

var (
	waitTimeout  time.Duration
	waitInterval time.Duration
	waitJSON     bool
	waitLogs     bool
	waitLogLines int
)

// exitError makes main exit with a specific code.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func waitErrorf(code int, format string, args ...interface{}) error {
	return &exitError{code: code, err: fmt.Errorf(format, args...)}
}

// sleepFunc waits for d or until ctx is done; tests replace it to avoid real delays.
var sleepFunc = func(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func init() {
	rootCmd.AddCommand(waitCmd)
	waitCmd.Flags().DurationVar(&waitTimeout, "timeout", 30*time.Minute, "give up after this long (0 = wait forever)")
	waitCmd.Flags().DurationVar(&waitInterval, "interval", 15*time.Second, "how often to poll GitHub")
	waitCmd.Flags().BoolVar(&waitJSON, "json", false, "print the result as JSON on stdout")
	waitCmd.Flags().BoolVar(&waitLogs, "logs", false, "on failure, also print the end of the log of each failed GitHub Actions job")
	waitCmd.Flags().IntVar(&waitLogLines, "log-lines", github.DefaultLogLines, "lines of each failed job's log to show with --logs")
}

var waitCmd = &cobra.Command{
	Use:   "wait <PR_URL>",
	Short: "Wait until a PR's CI finishes and exit with its result",
	Long: `Block until the CI of a pull request finishes, then exit with a code that
reflects the result. The PR does not need to be on the watch list.

It fails fast: as soon as any check fails, prw exits with code 1 even if other
checks are still running. If someone pushes to the PR while waiting, it follows
the new head commit.

Exit codes:
  0    CI succeeded (or the PR was merged while waiting, e.g. by auto-merge)
  1    CI failed (failure or error)
  2    timed out while CI was still pending
  3    other error (invalid URL, API error, PR closed without merging)
  4    the PR has no CI: no checks or statuses reported within 3 minutes of its
       last update (e.g. the repository has no CI, or none runs for its base branch)
  130  interrupted (Ctrl+C)

Progress goes to stderr; the result goes to stdout (as JSON with --json). With
--logs, a failure also prints the output of the step that failed in each failed
GitHub Actions job (failed_logs in --json).`,
	Example: `  prw wait https://github.com/owner/repo/pull/123
  prw wait https://github.com/owner/repo/pull/123 --timeout 10m --json
  prw wait https://github.com/owner/repo/pull/123 --logs
  prw wait "$PR_URL" && echo "green, deploying"`,
	SilenceUsage: true,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return waitErrorf(exitWaitError, "expected exactly one PR URL, got %d argument(s)", len(args))
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		ref, err := forge.ParsePRURL(args[0])
		if err != nil {
			return waitErrorf(exitWaitError, "invalid PR URL: %v", err)
		}
		owner, repo := ref.Owner, ref.Repo
		if waitInterval <= 0 {
			return waitErrorf(exitWaitError, "--interval must be positive")
		}
		if waitTimeout < 0 {
			return waitErrorf(exitWaitError, "--timeout must not be negative")
		}

		cfg, err := config.Load()
		if err != nil {
			return waitErrorf(exitWaitError, "failed to load config: %v", err)
		}
		client, err := clientFor(cfg, ref.Host)
		if err != nil {
			return &exitError{code: exitWaitError, err: err}
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if waitTimeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, waitTimeout)
			defer cancel()
		}
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)
		go func() {
			select {
			case <-sigCh:
				cancel()
			case <-ctx.Done():
			}
		}()

		result, err := waitForCI(ctx, client, ref, waitInterval, os.Stderr)
		if err != nil {
			return &exitError{code: exitWaitError, err: fmt.Errorf("%w%s", err, bitbucketAuthHint(ref.Host, err))}
		}
		if waitLogs && (result.State == "failure" || result.State == "error") {
			// A fresh context: the wait's may be about to time out.
			logCtx, cancelLogs := context.WithTimeout(context.Background(), time.Minute)
			result.FailedLogs, err = fetchFailedLogs(logCtx, client, owner, repo, result.SHA, waitLogLines)
			cancelLogs()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to fetch logs: %v\n", err)
			}
		}
		if err := printWaitResult(os.Stdout, result, waitJSON); err != nil {
			return &exitError{code: exitWaitError, err: err}
		}
		if !waitJSON {
			printFailedLogs(os.Stdout, result.FailedLogs)
		}
		return result.exitErr()
	},
}

// waitResult is the outcome of waitForCI; it is also the --json output.
type waitResult struct {
	Host         string               `json:"host,omitempty"` // "bitbucket.org" for Bitbucket; empty for GitHub
	Owner        string               `json:"owner"`
	Repo         string               `json:"repo"`
	Number       int                  `json:"number"`
	Title        string               `json:"title,omitempty"`
	URL          string               `json:"url"`
	SHA          string               `json:"sha,omitempty"`
	State        string               `json:"state"` // success, failure, error, pending, merged, closed
	FailedChecks []github.FailedCheck `json:"failed_checks,omitempty"`
	// FailedLogs has the log excerpt of each failed GitHub Actions job (--logs).
	FailedLogs    []github.FailedJobLog `json:"failed_logs,omitempty"`
	TotalChecks   int                   `json:"total_checks"`
	PendingChecks int                   `json:"pending_checks"`
	// MergeState is GitHub's mergeability (ready, conflict, blocked, behind, unstable,
	// draft); empty while GitHub is still computing it.
	MergeState     string  `json:"merge_state,omitempty"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	TimedOut       bool    `json:"timed_out,omitempty"`
	Interrupted    bool    `json:"interrupted,omitempty"`
}

// exitErr maps the result to the command's exit code (nil means exit 0).
func (r *waitResult) exitErr() error {
	pr := fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number)
	switch {
	case r.Interrupted:
		return waitErrorf(exitWaitInterrupted, "interrupted while waiting for %s", pr)
	case r.TimedOut:
		return waitErrorf(exitWaitTimeout, "timed out after %s; CI of %s is still %s", formatElapsed(r.ElapsedSeconds), pr, r.State)
	}
	switch r.State {
	case "success", "merged":
		return nil
	case "failure", "error":
		return waitErrorf(exitWaitFailed, "CI of %s finished with %s (%d failed check(s))", pr, r.State, len(r.FailedChecks))
	case "closed":
		return waitErrorf(exitWaitError, "%s was closed without merging", pr)
	case github.StateNoCI:
		// Not a success: `prw wait && deploy` must not deploy a PR that no CI looked at.
		return waitErrorf(exitWaitNoCI, "%s has no CI: no checks or statuses were reported", pr)
	}
	return waitErrorf(exitWaitError, "unexpected state %q for %s", r.State, pr)
}

// ciClient is the subset of the GitHub client used by waitForCI.
type ciClient interface {
	GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error)
	github.CIFetcher
}

// waitForCI polls until the PR's CI finishes, the PR is merged or closed, or ctx is
// done (timeout or interrupt). It only returns an error for non-transient failures.
func waitForCI(ctx context.Context, client ciClient, ref forge.Ref, interval time.Duration, progress io.Writer) (*waitResult, error) {
	owner, repo, number := ref.Owner, ref.Repo, ref.Number
	start := time.Now()
	result := &waitResult{Host: forge.StorageHost(ref.Host), Owner: owner, Repo: repo, Number: number, URL: ref.URL(), State: "pending"}
	finish := func() *waitResult {
		result.ElapsedSeconds = time.Since(start).Seconds()
		return result
	}

	stopped := func() *waitResult {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.TimedOut = true
		} else {
			result.Interrupted = true
		}
		return finish()
	}

	var lastLine string
	consecutiveErrors := 0
	for {
		delay := interval
		state, err := pollOnce(ctx, client, result)
		moved := false
		if err == nil && state != "pending" {
			// Errors confirming the head are handled like poll errors: retried, or
			// reported as a timeout / interruption, never taken as "head unchanged".
			moved, err = headMoved(ctx, client, result)
		}
		if err != nil && ctx.Err() != nil {
			return stopped(), nil // the timeout or Ctrl-C cancelled the poll's requests
		}
		if limited, resetAt := github.IsRateLimited(err); limited {
			// Not a failure of the PR or the network: wait for the quota to reset
			// (still bounded by --timeout) instead of burning the retry budget.
			delay = time.Until(resetAt)
			if delay < interval {
				delay = interval
			}
			fmt.Fprintf(progress, "⚠️  GitHub API rate limit reached; waiting %s before polling again\n", delay.Round(time.Second))
		} else if err != nil {
			if !isTransient(err) {
				return nil, err
			}
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveWaitErrors {
				return nil, fmt.Errorf("giving up after %d consecutive errors: %w", consecutiveErrors, err)
			}
			fmt.Fprintf(progress, "⚠️  %v (retrying)\n", err)
		} else {
			consecutiveErrors = 0
			if moved {
				fmt.Fprintf(progress, "↻ %s/%s#%d: new commit pushed; waiting for its CI\n", owner, repo, number)
				continue
			}
			if state != "pending" {
				return finish(), nil
			}
			line := fmt.Sprintf("⏳ %s/%s#%d: %s", owner, repo, number, pendingSummary(result))
			if line != lastLine {
				fmt.Fprintln(progress, line)
				lastLine = line
			}
		}

		sleepFunc(ctx, delay)
		if ctx.Err() != nil {
			return stopped(), nil
		}
	}
}

// headMoved reports whether a new commit was pushed to the PR since result was
// polled. The CI lookup follows the PR lookup, so a push in between would otherwise
// return the result of the previous commit. Merged or closed PRs don't move.
// Unchanged PRs answer 304, so this costs no quota.
func headMoved(ctx context.Context, client ciClient, result *waitResult) (bool, error) {
	if result.State == "merged" || result.State == "closed" {
		return false, nil
	}
	pr, err := client.GetPullRequest(ctx, result.Owner, result.Repo, result.Number)
	if err != nil {
		return false, fmt.Errorf("failed to confirm the PR head: %w", err)
	}
	return pr.Head.SHA != result.SHA, nil
}

// pollOnce refreshes result from GitHub and returns the current state.
func pollOnce(ctx context.Context, client ciClient, result *waitResult) (string, error) {
	pr, err := client.GetPullRequest(ctx, result.Owner, result.Repo, result.Number)
	if err != nil {
		return "", fmt.Errorf("failed to fetch PR: %w", err)
	}
	if pr.Title != "" {
		result.Title = pr.Title
	}
	result.SHA = pr.Head.SHA
	result.MergeState = github.MergeStatus(pr)

	if pr.IsClosed() {
		result.State = "closed"
		if pr.Merged {
			result.State = "merged"
		}
		result.FailedChecks = nil
		return result.State, nil
	}

	ci, err := github.FetchCIStatus(ctx, client, result.Owner, result.Repo, pr.Head.SHA)
	if err != nil {
		return "", err
	}
	github.ApplyNoCI(ci, pr.UpdatedAt, time.Now())
	result.State = ci.State
	result.FailedChecks = ci.FailedChecks
	result.TotalChecks = ci.Total
	result.PendingChecks = ci.Pending
	return ci.State, nil
}

// isTransient reports whether err is worth retrying: network errors, rate limiting
// and server-side errors. Client errors such as 401 or 404 are not.
func isTransient(err error) bool {
	var apiErr *github.APIError
	if !errors.As(err, &apiErr) {
		return true // network error, timeout, invalid JSON
	}
	// The client tells 403 rate limits apart from 403 missing permissions.
	return apiErr.RateLimited || apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
}

func pendingSummary(r *waitResult) string {
	sha := r.SHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	if r.TotalChecks == 0 {
		return fmt.Sprintf("waiting for CI to start on %s", sha)
	}
	return fmt.Sprintf("%d of %d check(s) still running on %s", r.PendingChecks, r.TotalChecks, sha)
}

func formatElapsed(seconds float64) string {
	return (time.Duration(seconds) * time.Second).Round(time.Second).String()
}

func printWaitResult(w io.Writer, r *waitResult, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}

	icon := "⏳"
	switch r.State {
	case "success", "merged":
		icon = "✅"
	case "failure", "error", "closed":
		icon = "❌"
	case github.StateNoCI:
		icon = "⚪"
	}
	header := fmt.Sprintf("%s %s/%s#%d", icon, r.Owner, r.Repo, r.Number)
	if r.Title != "" {
		header += " — " + r.Title
	}
	fmt.Fprintln(w, header)

	elapsed := formatElapsed(r.ElapsedSeconds)
	switch {
	case r.Interrupted:
		fmt.Fprintf(w, "Interrupted after %s; CI was %s\n", elapsed, r.State)
	case r.TimedOut:
		fmt.Fprintf(w, "Timed out after %s; CI is still %s\n", elapsed, r.State)
	case r.State == "merged" || r.State == "closed":
		fmt.Fprintf(w, "PR was %s (after %s)\n", r.State, elapsed)
	case r.State == github.StateNoCI:
		fmt.Fprintf(w, "No CI: no checks or statuses were reported for this PR (after %s)\n", elapsed)
	default:
		fmt.Fprintf(w, "CI: %s (%d check(s), %s)\n", r.State, r.TotalChecks, elapsed)
	}

	if len(r.FailedChecks) > 0 {
		fmt.Fprintln(w, "Failed checks:")
		for _, check := range r.FailedChecks {
			if check.URL != "" {
				fmt.Fprintf(w, "  ❌ %s  %s\n", check.Name, check.URL)
			} else {
				fmt.Fprintf(w, "  ❌ %s\n", check.Name)
			}
		}
	}
	fmt.Fprintln(w, r.URL)
	return nil
}
