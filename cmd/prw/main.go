package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/dashboard"
	"github.com/devblac/prw/internal/forge"
	"github.com/devblac/prw/internal/github"
	"github.com/devblac/prw/internal/notify"
	"github.com/devblac/prw/internal/version"
	"github.com/devblac/prw/internal/watcher"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		var exitErr *exitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.code)
		}
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "prw",
	Short: "Pull Request Watcher - monitor GitHub PR status changes",
	Long: `prw is a CLI tool that watches GitHub pull requests and notifies you
when their CI status changes (pending → success/failure).

If you are logged in with the GitHub CLI ('gh auth login'), prw uses that token
automatically. Otherwise set GITHUB_TOKEN (or GH_TOKEN), or store a token with
'prw config set github_token <token>'.`,
}

func init() {
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(unwatchCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(completionCmd)

	listCmd.Flags().BoolVar(&listJSON, "json", false, "output watched PRs as JSON")
	watchCmd.Flags().BoolVar(&watchMine, "mine", false, "automatically watch all your open PRs")
	watchCmd.Flags().BoolVar(&watchReviewRequested, "review-requested", false, "automatically watch open PRs where your review is requested")
	unwatchCmd.Flags().BoolVar(&unwatchMine, "mine", false, "stop auto-watching your open PRs and remove the PRs it added")
	unwatchCmd.Flags().BoolVar(&unwatchReviewRequested, "review-requested", false, "stop auto-watching review requests and remove the PRs it added")
	runCmd.Flags().StringVar(&notifyFilter, "on", "", "notify on: change, fail, or success")
	runCmd.Flags().BoolVar(&runOnce, "once", false, "check watched PRs once and exit")
	runCmd.Flags().BoolVar(&notifyNative, "notify-native", false, "enable native OS notifications (macOS/Linux/Windows)")
}

var (
	listJSON     bool
	notifyFilter string
	runOnce      bool
	notifyNative bool
)

// errMissingToken is returned by commands that need GitHub access when no token is found.
var errMissingToken = errors.New("missing GITHUB_TOKEN; run 'gh auth login', set GITHUB_TOKEN, or configure it with 'prw config set github_token <token>'")

// newGitHubClient allows tests to inject a custom GitHub client.
var newGitHubClient = github.NewClient

var (
	watchMine              bool
	watchReviewRequested   bool
	unwatchMine            bool
	unwatchReviewRequested bool
)

// autoWatchFlags returns the auto-watch sources selected by a command's flags.
func autoWatchFlags(mine, reviewRequested bool) []string {
	var sources []string
	if mine {
		sources = append(sources, config.AutoWatchMine)
	}
	if reviewRequested {
		sources = append(sources, config.AutoWatchReviewRequested)
	}
	return sources
}

// prURLOrAutoWatchArgs requires a PR URL unless an auto-watch flag is set.
func prURLOrAutoWatchArgs(flagsSet func() bool) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if flagsSet() {
			return cobra.MaximumNArgs(1)(cmd, args)
		}
		if len(args) != 1 {
			return fmt.Errorf("expected a PR URL (or --mine / --review-requested), got %d argument(s)", len(args))
		}
		return nil
	}
}

var watchCmd = &cobra.Command{
	Use:   "watch [PR_URL]",
	Short: "Add a PR to the watch list, or follow your PRs automatically",
	Long: `Add a pull request to the watch list.

With --mine or --review-requested, prw instead finds your PRs on GitHub and on
Bitbucket Cloud (when a Bitbucket token or bitbucket_user is set) and keeps the
watch list up to date by itself: matching PRs are added right away, and
'prw run' searches again every 5 minutes.

  --mine              your open PRs, in every repository (on Bitbucket, in every
                      workspace, or those in bitbucket_workspaces)
  --review-requested  open PRs where your review is requested (on GitHub incl.
                      your teams; on Bitbucket, PRs listing you as a reviewer in
                      repositories active in the last 30 days, rescanned every
                      15 minutes)

PRs added by --review-requested are dropped again once your review is no longer
requested (on Bitbucket: once you approve or request changes). Merged or closed PRs are always dropped by 'prw run' after it notifies.
Undo with 'prw unwatch --mine' / 'prw unwatch --review-requested'.`,
	Example: `  prw watch https://github.com/owner/repo/pull/123
  prw watch --mine
  prw watch --mine --review-requested`,
	Args: prURLOrAutoWatchArgs(func() bool { return watchMine || watchReviewRequested }),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			if err := watchSinglePR(args[0]); err != nil {
				return err
			}
		}
		if sources := autoWatchFlags(watchMine, watchReviewRequested); len(sources) > 0 {
			return enableAutoWatch(sources)
		}
		return nil
	},
}

func watchSinglePR(prURL string) error {
	ref, err := forge.ParsePRURL(prURL)
	if err != nil {
		return fmt.Errorf("invalid PR URL: %w", err)
	}
	owner, repo, number := ref.Owner, ref.Repo, ref.Number

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, err := clientFor(cfg, ref.Host)
	if err != nil {
		return err
	}

	// Try to fetch the PR to validate it exists and get title
	pr, err := client.GetPullRequest(context.Background(), owner, repo, number)
	if err != nil {
		return fmt.Errorf("failed to fetch PR: %w%s", err, bitbucketAuthHint(ref.Host, err))
	}

	watchedPR := config.WatchedPR{
		Host:   forge.StorageHost(ref.Host),
		Owner:  owner,
		Repo:   repo,
		Number: number,
		Title:  pr.Title,
	}

	// Re-read the config under a lock instead of saving the copy loaded before the
	// network call: a running `prw run` may have updated the file in the meantime.
	added, claimed := false, false
	if err := config.Update(func(c *config.Config) error {
		added = c.AddPR(watchedPR)
		if !added {
			// Watching by hand a PR that auto-watch added keeps it even if that
			// auto-watch is turned off later.
			claimed = c.MarkManual(ref.Host, owner, repo, number)
		}
		if !added && !claimed {
			return config.ErrNoChanges
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	if !added {
		fmt.Printf("PR %s/%s#%d is already being watched.\n", owner, repo, number)
		return nil
	}

	fmt.Printf("Now watching: %s/%s#%d - %s\n", owner, repo, number, pr.Title)
	return nil
}

func enableAutoWatch(sources []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	bbAuth, _ := cfg.ResolveBitbucketAuth()
	finders := autoWatchFinders(cfg, newBitbucketClient(bbAuth.Email, bbAuth.Token))
	if len(finders) == 0 {
		return fmt.Errorf("%w; for Bitbucket, set 'prw config set bitbucket_email' and 'prw config set bitbucket_token'", errMissingToken)
	}

	var enabled []string
	if err := config.Update(func(c *config.Config) error {
		for _, source := range sources {
			if c.EnableAutoWatch(source) {
				enabled = append(enabled, source)
			}
		}
		if len(enabled) == 0 {
			return config.ErrNoChanges
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	for _, source := range sources {
		state := "Now auto-watching"
		if !contains(enabled, source) {
			state = "Already auto-watching"
		}
		fmt.Printf("%s %s.\n", state, watcher.AutoWatchDescription[source])
	}

	// Run the searches now so the user sees the result immediately.
	res, err := watcher.Sync(context.Background(), finders, sources)
	for _, warning := range res.Warnings {
		fmt.Printf("Warning: %s\n", warning)
	}
	if err != nil {
		return fmt.Errorf("auto-watch is enabled, but searching failed (prw run will retry): %w", err)
	}
	if len(res.Added) == 0 {
		fmt.Println("No new PRs to add right now.")
	}
	for _, pr := range res.Added {
		fmt.Printf("  + %s - %s\n", watcher.DisplayPR(pr), pr.Title)
	}
	for _, pr := range res.Removed {
		fmt.Printf("  - %s (review no longer requested)\n", watcher.DisplayPR(pr))
	}
	fmt.Println("'prw run' keeps this list up to date (searches again every 5 minutes).")
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all watched PRs",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if listJSON {
			return outputJSONList(cfg.WatchedPRs)
		}

		for _, source := range cfg.AutoWatch {
			fmt.Printf("Auto-watching %s (--%s).\n", watcher.AutoWatchDescription[source], source)
		}
		if len(cfg.WatchedPRs) == 0 {
			fmt.Println("No PRs being watched.")
			return nil
		}
		if len(cfg.AutoWatch) > 0 {
			fmt.Println()
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "REPO\tPR\tSTATUS\tREVIEW\tMERGE\tLAST CHECKED\tADDED BY\tTITLE")
		fmt.Fprintln(w, "----\t--\t------\t------\t-----\t------------\t--------\t-----")

		for _, pr := range cfg.WatchedPRs {
			repo := fmt.Sprintf("%s/%s", pr.Owner, pr.Repo)
			switch {
			case forge.IsBitbucket(pr.Host):
				repo = "bitbucket:" + repo
			case forge.IsEnterprise(pr.Host):
				repo = forge.NormalizeHost(pr.Host) + ":" + repo
			}
			prNum := fmt.Sprintf("#%d", pr.Number)
			status := pr.LastKnownState
			if status == "" {
				status = "unknown"
			}
			lastChecked := "never"
			if !pr.LastChecked.IsZero() {
				lastChecked = pr.LastChecked.Format("2006-01-02 15:04")
			}
			title := pr.Title
			if len(title) > 50 {
				title = title[:47] + "..."
			}
			addedBy := "you"
			if pr.Source != "" {
				addedBy = "--" + pr.Source
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", repo, prNum, status, orDash(pr.LastReviewState), orDash(pr.LastMergeState), lastChecked, addedBy, title)
		}

		w.Flush()
		return nil
	},
}

var unwatchCmd = &cobra.Command{
	Use:   "unwatch [PR_URL]",
	Short: "Remove a PR from the watch list, or stop following your PRs",
	Long: `Remove a pull request from the watch list.

With --mine or --review-requested, stop that auto-watch and remove the PRs it
added. PRs you added by hand with 'prw watch <PR_URL>' are kept.

Unwatching a single PR that auto-watch added only lasts until the next search
finds it again; use --mine / --review-requested to stop following those PRs.`,
	Example: `  prw unwatch https://github.com/owner/repo/pull/123
  prw unwatch --review-requested`,
	Args: prURLOrAutoWatchArgs(func() bool { return unwatchMine || unwatchReviewRequested }),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			if err := unwatchSinglePR(args[0]); err != nil {
				return err
			}
		}
		if sources := autoWatchFlags(unwatchMine, unwatchReviewRequested); len(sources) > 0 {
			return disableAutoWatch(sources)
		}
		return nil
	},
}

func unwatchSinglePR(prURL string) error {
	ref, err := forge.ParsePRURL(prURL)
	if err != nil {
		return fmt.Errorf("invalid PR URL: %w", err)
	}
	owner, repo, number := ref.Owner, ref.Repo, ref.Number

	removed := false
	if err := config.Update(func(c *config.Config) error {
		removed = c.RemovePR(ref.Host, owner, repo, number)
		if !removed {
			return config.ErrNoChanges
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	if !removed {
		fmt.Printf("PR %s/%s#%d is not being watched.\n", owner, repo, number)
		return nil
	}

	fmt.Printf("Stopped watching: %s/%s#%d\n", owner, repo, number)
	return nil
}

func disableAutoWatch(sources []string) error {
	type outcome struct {
		wasOn   bool
		removed []config.WatchedPR
	}
	results := map[string]outcome{}
	if err := config.Update(func(c *config.Config) error {
		changed := false
		for _, source := range sources {
			wasOn, removed := c.DisableAutoWatch(source)
			results[source] = outcome{wasOn, removed}
			if wasOn || len(removed) > 0 {
				changed = true
			}
		}
		if !changed {
			return config.ErrNoChanges
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	for _, source := range sources {
		r := results[source]
		if !r.wasOn && len(r.removed) == 0 {
			fmt.Printf("Was not auto-watching %s.\n", watcher.AutoWatchDescription[source])
			continue
		}
		fmt.Printf("Stopped auto-watching %s; removed %d PR(s) it had added.\n", watcher.AutoWatchDescription[source], len(r.removed))
	}
	return nil
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the watcher loop",
	Long: `Start monitoring all watched PRs for status changes.
Polls GitHub API on the configured interval and notifies on changes.
Press Ctrl+C to stop.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// GitHub needs a token; without one, only Bitbucket PRs can be watched.
		var client watcher.GitHubClient
		if token := cfg.GetToken(); token != "" {
			client = newGitHubClient(token)
		} else if needsGitHub(cfg) {
			return errMissingToken
		}

		// Build notifier chain
		notifiers := []notify.Notifier{notify.NewConsoleNotifier()}
		if cfg.WebhookURL != "" {
			notifiers = append(notifiers, notify.NewWebhookNotifierWithFormat(cfg.WebhookURL, cfg.WebhookFormat))
		}
		// Add native notifications if enabled via flag or config
		if notifyNative || cfg.NotificationNative {
			notifiers = append(notifiers, notify.NewNativeNotifier())
		}
		notifier := notify.NewMultiNotifier(notifiers...)

		filter := cfg.NotificationFilter
		if notifyFilter != "" {
			filter = config.NormalizeNotificationFilter(notifyFilter)
			if !config.IsValidNotificationFilter(filter) {
				return fmt.Errorf("invalid --on value %q (expected change, fail, or success)", notifyFilter)
			}
		}
		cfg.NotificationFilter = filter

		w := watcher.New(client, cfg, notifier)
		bbAuth, _ := cfg.ResolveBitbucketAuth()
		bb := newBitbucketClient(bbAuth.Email, bbAuth.Token)
		w.SetClient(forge.Bitbucket, bb)
		if f := bitbucketFinder(cfg, bb); f != nil {
			w.AddFinder(f)
		}
		if host := cfg.EnterpriseHost(); host != "" {
			ghe, err := enterpriseClient(cfg, host)
			if err != nil {
				fmt.Printf("Warning: %v\n", err)
			} else {
				w.SetClient(host, ghe)
				w.AddFinder(watcher.GitHubFinder{Searcher: ghe, HostName: host})
			}
		}

		// Setup signal handling
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

		go func() {
			<-sigCh
			cancel()
		}()

		if runOnce {
			return w.RunOnce(ctx)
		}

		return w.Run(ctx)
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		path, err := config.ConfigPath()
		if err != nil {
			return fmt.Errorf("failed to determine config path: %w", err)
		}
		fmt.Printf("Config file: %s\n\n", path)
		fmt.Printf("poll_interval_seconds: %d\n", cfg.PollIntervalSeconds)
		fmt.Printf("webhook_url: %s\n", cfg.WebhookURL)
		fmt.Printf("webhook_format: %s\n", notify.NormalizeWebhookFormat(cfg.WebhookFormat))
		fmt.Printf("notification_filter: %s\n", cfg.NotificationFilter)
		fmt.Printf("notification_native: %v\n", cfg.NotificationNative)
		fmt.Printf("pr_events: %v\n", !cfg.DisablePREvents)

		_, tokenSource := cfg.ResolveToken()
		fmt.Printf("github_token: %s\n", tokenSource)
		if host := cfg.EnterpriseHost(); host != "" {
			_, gheSource := cfg.ResolveEnterpriseToken()
			fmt.Printf("github_enterprise_url: %s\n", cfg.GitHubEnterpriseURL)
			fmt.Printf("github_enterprise_token: %s\n", gheSource)
		}
		bbAuth, bbSource := cfg.ResolveBitbucketAuth()
		switch {
		case bbAuth.Token == "":
			fmt.Printf("bitbucket_token: %s (public repositories only)\n", bbSource)
		case bbAuth.Email != "":
			fmt.Printf("bitbucket_token: %s, API token for %s\n", bbSource, bbAuth.Email)
		default:
			fmt.Printf("bitbucket_token: %s, access token\n", bbSource)
		}

		autoWatch := "off"
		if len(cfg.AutoWatch) > 0 {
			autoWatch = strings.Join(cfg.AutoWatch, ", ")
		}
		fmt.Printf("auto_watch: %s\n", autoWatch)

		fmt.Printf("\nWatched PRs: %d\n", len(cfg.WatchedPRs))

		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Long: `Set a configuration value.
Supported keys:
  - poll_interval_seconds: polling interval in seconds (default: 20)
  - webhook_url: URL to POST notifications to
  - webhook_format: auto, slack, discord, or generic (default: auto, detected from the URL)
  - github_token: GitHub personal access token
  - github_enterprise_url: web address of your GitHub Enterprise Server or GHE.com
    instance (e.g. https://github.acme.com), watched alongside github.com
  - github_enterprise_token: its token (default: GH_ENTERPRISE_TOKEN, or 'gh auth token --hostname <host>')
  - bitbucket_token: Bitbucket Cloud API token (with bitbucket_email), or an access token alone
  - bitbucket_email: the Atlassian account email that goes with an API token
  - bitbucket_workspaces: workspaces 'prw dashboard' looks in, comma-separated (default: all of yours)
  - bitbucket_user: whose Bitbucket PRs 'prw dashboard' shows, UUID or account id (default: the token's owner)
  - group_by: how 'prw dashboard' groups PRs into tasks: auto, issue, branch, title or none (default: auto)
  - issue_pattern: regexp for issue keys in branches and titles (default: [A-Z][A-Z0-9]{1,9}-[0-9]+)
  - branch_order: target branches in promotion order, comma-separated, e.g. dev,qa,main
  - notification_filter: change, fail, or success
  - notification_native: enable native OS notifications (true/false)
  - pr_events: notify about reviews and merge conflicts / readiness, not just CI (true/false, default: true)`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		value := args[1]

		if err := config.Update(func(cfg *config.Config) error {
			return applyConfigSet(cfg, key, value)
		}); err != nil {
			return err
		}

		if strings.HasSuffix(key, "_token") {
			fmt.Printf("Set %s\n", key) // don't echo secrets into terminal scrollback
		} else {
			fmt.Printf("Set %s = %s\n", key, value)
		}
		return nil
	},
}

func applyConfigSet(cfg *config.Config, key, value string) error {
	switch key {
	case "poll_interval_seconds":
		interval, err := strconv.Atoi(value)
		if err != nil || interval <= 0 {
			return fmt.Errorf("poll_interval_seconds must be a positive integer")
		}
		cfg.PollIntervalSeconds = interval
	case "webhook_url":
		cfg.WebhookURL = value
	case "webhook_format":
		format := notify.NormalizeWebhookFormat(value)
		if !notify.IsValidWebhookFormat(format) {
			return fmt.Errorf("webhook_format must be one of: auto, slack, discord, generic")
		}
		cfg.WebhookFormat = format
	case "github_token":
		cfg.GitHubToken = value
	case "github_enterprise_url":
		if _, err := github.EnterpriseAPIURL(value); err != nil {
			return err
		}
		if forge.IsBitbucket(value) || forge.NormalizeHost(hostOf(value)) == forge.GitHub {
			return fmt.Errorf("github_enterprise_url is for GitHub Enterprise; github.com needs no setting")
		}
		cfg.GitHubEnterpriseURL = strings.TrimRight(strings.TrimSpace(value), "/")
	case "github_enterprise_token":
		cfg.GitHubEnterpriseToken = value
	case "bitbucket_token":
		cfg.BitbucketToken = value
	case "bitbucket_email":
		cfg.BitbucketEmail = value
	case "bitbucket_workspaces":
		cfg.BitbucketWorkspaces = splitList(value)
	case "bitbucket_user":
		cfg.BitbucketUser = strings.TrimSpace(value)
	case "group_by":
		if !dashboard.ValidGroupBy(value) {
			return fmt.Errorf("group_by must be one of: auto, issue, branch, title, none")
		}
		cfg.GroupBy = value
	case "issue_pattern":
		if _, err := dashboard.NewGrouper(dashboard.GroupAuto, value); err != nil {
			return err
		}
		cfg.IssuePattern = value
	case "branch_order":
		cfg.BranchOrder = splitList(value)
	case "notification_filter":
		filter := config.NormalizeNotificationFilter(value)
		if !config.IsValidNotificationFilter(filter) {
			return fmt.Errorf("notification_filter must be one of: change, fail, success")
		}
		cfg.NotificationFilter = filter
	case "notification_native":
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("notification_native must be true or false")
		}
		cfg.NotificationNative = enabled
	case "pr_events":
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("pr_events must be true or false")
		}
		cfg.DisablePREvents = !enabled
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

var configUnsetCmd = &cobra.Command{
	Use:   "unset <key>",
	Short: "Unset a configuration value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]

		if err := config.Update(func(cfg *config.Config) error {
			return applyConfigUnset(cfg, key)
		}); err != nil {
			return err
		}

		fmt.Printf("Unset %s\n", key)
		return nil
	},
}

func applyConfigUnset(cfg *config.Config, key string) error {
	switch key {
	case "poll_interval_seconds":
		cfg.PollIntervalSeconds = 20 // reset to default
	case "webhook_url":
		cfg.WebhookURL = ""
	case "webhook_format":
		cfg.WebhookFormat = ""
	case "github_token":
		cfg.GitHubToken = ""
	case "github_enterprise_url":
		cfg.GitHubEnterpriseURL = ""
	case "github_enterprise_token":
		cfg.GitHubEnterpriseToken = ""
	case "bitbucket_token":
		cfg.BitbucketToken = ""
	case "bitbucket_email":
		cfg.BitbucketEmail = ""
	case "bitbucket_workspaces":
		cfg.BitbucketWorkspaces = nil
	case "bitbucket_user":
		cfg.BitbucketUser = ""
	case "group_by":
		cfg.GroupBy = ""
	case "issue_pattern":
		cfg.IssuePattern = ""
	case "branch_order":
		cfg.BranchOrder = nil
	case "notification_filter":
		cfg.NotificationFilter = config.NotificationFilterChange
	case "notification_native":
		cfg.NotificationNative = false
	case "pr_events":
		cfg.DisablePREvents = false
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

// hostOf returns the host of a web address given with or without a scheme.
func hostOf(webURL string) string {
	raw := strings.TrimSpace(webURL)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("prw version %s\n", version.String())
	},
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configUnsetCmd)
}

type listPROutput struct {
	Host        string     `json:"host,omitempty"` // "bitbucket.org" for Bitbucket; empty for GitHub
	Owner       string     `json:"owner"`
	Repo        string     `json:"repo"`
	Number      int        `json:"number"`
	Status      string     `json:"status"`
	LastChecked *time.Time `json:"last_checked,omitempty"`
	Title       string     `json:"title,omitempty"`
	Source      string     `json:"source,omitempty"` // auto-watch source that added it; empty if added by hand
	Review      string     `json:"review,omitempty"` // approved, changes_requested or none; empty until checked
	Merge       string     `json:"merge,omitempty"`  // ready, conflict, blocked, behind, unstable or draft
}

// orDash shows a state that hasn't been observed yet as "-".
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func outputJSONList(prs []config.WatchedPR) error {
	output := make([]listPROutput, 0, len(prs))
	for _, pr := range prs {
		status := pr.LastKnownState
		if status == "" {
			status = "unknown"
		} else {
			status = github.NormalizeState(status)
		}
		var lastChecked *time.Time
		if !pr.LastChecked.IsZero() {
			t := pr.LastChecked
			lastChecked = &t
		}
		output = append(output, listPROutput{
			Host:        forge.StorageHost(pr.Host),
			Owner:       pr.Owner,
			Repo:        pr.Repo,
			Number:      pr.Number,
			Status:      status,
			LastChecked: lastChecked,
			Title:       pr.Title,
			Source:      pr.Source,
			Review:      pr.LastReviewState,
			Merge:       pr.LastMergeState,
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}
