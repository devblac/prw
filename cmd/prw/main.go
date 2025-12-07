package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
	"github.com/devblac/prw/internal/notify"
	"github.com/devblac/prw/internal/version"
	"github.com/devblac/prw/internal/watcher"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "prw",
	Short: "Pull Request Watcher - monitor GitHub PR status changes",
	Long: `prw is a CLI tool that watches GitHub pull requests and notifies you
when their CI status changes (pending → success/failure).

Configure your GitHub token via GITHUB_TOKEN environment variable or
in ~/.prw/config.json.`,
}

func init() {
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(unwatchCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(versionCmd)

	listCmd.Flags().BoolVar(&listJSON, "json", false, "output watched PRs as JSON")
	runCmd.Flags().StringVar(&notifyFilter, "on", "", "notify on: change, fail, or success")
}

var (
	listJSON     bool
	notifyFilter string
)

var watchCmd = &cobra.Command{
	Use:   "watch <PR_URL>",
	Short: "Add a PR to the watch list",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prURL := args[0]

		owner, repo, number, err := github.ParsePRURL(prURL)
		if err != nil {
			return fmt.Errorf("invalid PR URL: %w", err)
		}

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		token := cfg.GetToken()
		if token == "" {
			return fmt.Errorf("missing GITHUB_TOKEN; set it as an environment variable or configure it with 'prw config set github_token <token>'")
		}

		// Try to fetch the PR to validate it exists and get title
		client := github.NewClient(token)
		pr, err := client.GetPullRequest(owner, repo, number)
		if err != nil {
			return fmt.Errorf("failed to fetch PR: %w", err)
		}

		watchedPR := config.WatchedPR{
			Owner:  owner,
			Repo:   repo,
			Number: number,
			Title:  pr.Title,
		}

		if !cfg.AddPR(watchedPR) {
			fmt.Printf("PR %s/%s#%d is already being watched.\n", owner, repo, number)
			return nil
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Now watching: %s/%s#%d - %s\n", owner, repo, number, pr.Title)
		return nil
	},
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

		if len(cfg.WatchedPRs) == 0 {
			fmt.Println("No PRs being watched.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "REPO\tPR\tSTATUS\tLAST CHECKED\tTITLE")
		fmt.Fprintln(w, "----\t--\t------\t------------\t-----")

		for _, pr := range cfg.WatchedPRs {
			repo := fmt.Sprintf("%s/%s", pr.Owner, pr.Repo)
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
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", repo, prNum, status, lastChecked, title)
		}

		w.Flush()
		return nil
	},
}

var unwatchCmd = &cobra.Command{
	Use:   "unwatch <PR_URL>",
	Short: "Remove a PR from the watch list",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prURL := args[0]

		owner, repo, number, err := github.ParsePRURL(prURL)
		if err != nil {
			return fmt.Errorf("invalid PR URL: %w", err)
		}

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if !cfg.RemovePR(owner, repo, number) {
			fmt.Printf("PR %s/%s#%d is not being watched.\n", owner, repo, number)
			return nil
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Stopped watching: %s/%s#%d\n", owner, repo, number)
		return nil
	},
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

		token := cfg.GetToken()
		if token == "" {
			return fmt.Errorf("missing GITHUB_TOKEN; set it as an environment variable or configure it with 'prw config set github_token <token>'")
		}

		client := github.NewClient(token)

		// Build notifier chain
		notifiers := []notify.Notifier{notify.NewConsoleNotifier()}
		if cfg.WebhookURL != "" {
			notifiers = append(notifiers, notify.NewWebhookNotifier(cfg.WebhookURL))
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

		// Setup signal handling
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

		go func() {
			<-sigCh
			cancel()
		}()

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
		fmt.Printf("notification_filter: %s\n", cfg.NotificationFilter)

		tokenSource := "not set"
		if cfg.GitHubToken != "" {
			tokenSource = "config file"
		} else if os.Getenv("GITHUB_TOKEN") != "" {
			tokenSource = "environment variable"
		}
		fmt.Printf("github_token: %s\n", tokenSource)

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
  - github_token: GitHub personal access token
  - notification_filter: change, fail, or success`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		value := args[1]

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		switch key {
		case "poll_interval_seconds":
			interval, err := strconv.Atoi(value)
			if err != nil || interval <= 0 {
				return fmt.Errorf("poll_interval_seconds must be a positive integer")
			}
			cfg.PollIntervalSeconds = interval
		case "webhook_url":
			cfg.WebhookURL = value
		case "github_token":
			cfg.GitHubToken = value
		case "notification_filter":
			filter := config.NormalizeNotificationFilter(value)
			if !config.IsValidNotificationFilter(filter) {
				return fmt.Errorf("notification_filter must be one of: change, fail, success")
			}
			cfg.NotificationFilter = filter
		default:
			return fmt.Errorf("unknown config key: %s", key)
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Set %s = %s\n", key, value)
		return nil
	},
}

var configUnsetCmd = &cobra.Command{
	Use:   "unset <key>",
	Short: "Unset a configuration value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		switch key {
		case "poll_interval_seconds":
			cfg.PollIntervalSeconds = 20 // reset to default
		case "webhook_url":
			cfg.WebhookURL = ""
		case "github_token":
			cfg.GitHubToken = ""
		case "notification_filter":
			cfg.NotificationFilter = config.NotificationFilterChange
		default:
			return fmt.Errorf("unknown config key: %s", key)
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Unset %s\n", key)
		return nil
	},
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
	Owner       string     `json:"owner"`
	Repo        string     `json:"repo"`
	Number      int        `json:"number"`
	Status      string     `json:"status"`
	LastChecked *time.Time `json:"last_checked,omitempty"`
	Title       string     `json:"title,omitempty"`
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
			Owner:       pr.Owner,
			Repo:        pr.Repo,
			Number:      pr.Number,
			Status:      status,
			LastChecked: lastChecked,
			Title:       pr.Title,
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}
