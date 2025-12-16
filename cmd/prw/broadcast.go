package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
	"github.com/devblac/prw/internal/notify"
)

var (
	broadcastFilter  string
	broadcastWebhook string
	broadcastDryRun  bool
)

func init() {
	rootCmd.AddCommand(broadcastCmd)
	broadcastCmd.Flags().StringVar(&broadcastFilter, "filter", "all", "statuses to include: all, changed, failing")
	broadcastCmd.Flags().StringVar(&broadcastWebhook, "webhook", "", "override webhook URL for this broadcast")
	broadcastCmd.Flags().BoolVar(&broadcastDryRun, "dry-run", false, "print statuses without sending to webhook")
}

var broadcastCmd = &cobra.Command{
	Use:   "broadcast",
	Short: "Broadcast current PR statuses to console/webhook",
	Long:  "Fetch current statuses for all watched PRs and broadcast them to the console and/or webhook (Slack/Discord/etc).",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if len(cfg.WatchedPRs) == 0 {
			fmt.Println("No PRs being watched.")
			return nil
		}

		token := cfg.GetToken()
		if token == "" {
			return fmt.Errorf("missing GITHUB_TOKEN; set it as an environment variable or configure it with 'prw config set github_token <token>'")
		}

		filter := strings.ToLower(strings.TrimSpace(broadcastFilter))
		if filter == "" {
			filter = "all"
		}
		if filter != "all" && filter != "changed" && filter != "failing" {
			return fmt.Errorf("invalid --filter value %q (expected all, changed, or failing)", broadcastFilter)
		}

		client := newGitHubClient(token)

		webhookURL := broadcastWebhook
		if webhookURL == "" {
			webhookURL = cfg.WebhookURL
		}

		notifiers := []notify.Notifier{notify.NewConsoleNotifier()}
		if !broadcastDryRun && webhookURL != "" {
			notifiers = append(notifiers, notify.NewWebhookNotifier(webhookURL))
		}
		notifier := notify.NewMultiNotifier(notifiers...)

		var anySent bool
		for i := range cfg.WatchedPRs {
			pr := &cfg.WatchedPRs[i]

			ghPR, err := client.GetPullRequest(pr.Owner, pr.Repo, pr.Number)
			if err != nil {
				fmt.Printf("Error fetching PR %s/%s#%d: %v\n", pr.Owner, pr.Repo, pr.Number, err)
				continue
			}

			status, err := client.GetCombinedStatus(pr.Owner, pr.Repo, ghPR.Head.SHA)
			if err != nil {
				fmt.Printf("Error fetching status for %s/%s#%d: %v\n", pr.Owner, pr.Repo, pr.Number, err)
				continue
			}

			currentState := github.NormalizeState(status.State)
			previousState := github.NormalizeState(pr.LastKnownState)
			changed := previousState != "" && previousState != currentState

			// Refresh title
			if ghPR.Title != "" && ghPR.Title != pr.Title {
				pr.Title = ghPR.Title
			}

			// Apply filter
			switch filter {
			case "changed":
				if !changed {
					pr.LastKnownSHA = ghPR.Head.SHA
					pr.LastKnownState = currentState
					pr.LastChecked = time.Now()
					continue
				}
			case "failing":
				if currentState != "failure" && currentState != "error" {
					pr.LastKnownSHA = ghPR.Head.SHA
					pr.LastKnownState = currentState
					pr.LastChecked = time.Now()
					continue
				}
			}

			event := &notify.StatusChangeEvent{
				Owner:         pr.Owner,
				Repo:          pr.Repo,
				Number:        pr.Number,
				Title:         pr.Title,
				PreviousState: previousState,
				CurrentState:  currentState,
				SHA:           ghPR.Head.SHA,
				Timestamp:     time.Now(),
			}

			if broadcastDryRun {
				fmt.Printf("DRY RUN: %s/%s#%d status=%s (prev=%s)\n", pr.Owner, pr.Repo, pr.Number, currentState, previousState)
			} else {
				if err := notifier.Notify(event); err != nil {
					fmt.Printf("Warning: notification failed for %s/%s#%d: %v\n", pr.Owner, pr.Repo, pr.Number, err)
				} else {
					anySent = true
				}
			}

			// Update stored state
			pr.LastKnownSHA = ghPR.Head.SHA
			pr.LastKnownState = currentState
			pr.LastChecked = time.Now()
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		if broadcastDryRun {
			fmt.Println("Dry-run complete (no webhook calls made).")
		} else if !anySent {
			fmt.Println("No notifications sent (filter may have excluded all PRs).")
		}
		return nil
	},
}
