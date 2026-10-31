package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/dashboard"
)

var (
	dashMine   bool
	dashReview bool
	dashMerged bool
	dashSince  string
	// mergedWindow is the --since window, for the section title.
	mergedWindow time.Duration
	dashInterval time.Duration
	dashOnce     bool
	dashJSON     bool
	dashGroupBy  string
	dashWidth    int
	dashNoLinks  bool
)

func init() {
	rootCmd.AddCommand(dashboardCmd)
	f := dashboardCmd.Flags()
	f.BoolVar(&dashMine, "mine", true, "your open PRs (--mine=false to hide them)")
	f.BoolVar(&dashReview, "review", false, "PRs where your review is requested")
	f.BoolVar(&dashMerged, "merged", false, "also your recently merged PRs (see --since)")
	f.StringVar(&dashSince, "since", "24h", "with --merged, how far back: e.g. 24h, 3d")
	f.DurationVar(&dashInterval, "interval", time.Minute, "how often to refresh")
	f.BoolVar(&dashOnce, "once", false, "print once and exit")
	f.BoolVar(&dashJSON, "json", false, "print the PRs as JSON once and exit")
	f.StringVar(&dashGroupBy, "group-by", "", "group PRs into tasks by: auto, issue, branch, title or none (default: config group_by, or auto)")
	f.IntVar(&dashWidth, "width", 0, "table width (default: the terminal's)")
	f.BoolVar(&dashNoLinks, "no-links", false, "don't make PR numbers clickable links")
}

var dashboardCmd = &cobra.Command{
	Use:     "dashboard",
	Aliases: []string{"top"},
	Short:   "A live table of your PRs across repositories, grouped by task",
	Long: `Show your pull requests across every repository on GitHub and Bitbucket Cloud as a
live table that refreshes every minute, like top for PRs. You don't need to watch
them first: prw finds them.

Rows are tasks: PRs whose source branch or title share an issue key (PROJ-123), or
whose branches only differ by cherry-pick / backport decorations, share a row, so a
change and its cherry-picks to other branches show up together. Columns are target
branches in promotion order (dev, staging, rc/release, main/master), so you can see
at a glance how far each task got.

Each cell shows the PR number (a clickable link in most terminals) and its most
pressing state: ❌ CI failed, ⛔ conflict, 📝 changes requested, ⏳ CI running
(done/total), ✅ CI ok, 🚀 ready to merge, 🟣 merged; ✓N counts approvals.

Requests are kept low: a PR's CI is only read again when its head changes, while it
is running, or every 15 minutes. That fits dozens of Bitbucket PRs in its 1,000
requests per hour; the footer shows how many were used.

Tune grouping with 'prw config set group_by' / 'issue_pattern' and the column order
with 'prw config set branch_order dev,qa,main'.`,
	Example: `  prw dashboard
  prw dashboard --review --merged
  prw dashboard --merged --since 3d --group-by branch
  prw dashboard --once --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		filter := dashboard.Filter{Mine: dashMine, Review: dashReview}
		if dashMerged {
			window, err := parseWindow(dashSince)
			if err != nil {
				return err
			}
			filter.MergedSince = time.Now().Add(-window)
			mergedWindow = window
		}
		if !filter.Mine && !filter.Review && !dashMerged {
			return fmt.Errorf("nothing to show: --mine=false needs --review or --merged")
		}
		groupBy := dashGroupBy
		if groupBy == "" {
			groupBy = cfg.GroupBy
		}
		grouper, err := dashboard.NewGrouper(groupBy, cfg.IssuePattern)
		if err != nil {
			return err
		}
		sources, err := dashboardSources(cfg)
		if err != nil {
			return err
		}
		if dashInterval < 10*time.Second {
			return fmt.Errorf("--interval must be at least 10s")
		}

		collector := dashboard.NewCollector(sources...)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		if dashJSON {
			items, warnings := collector.Refresh(ctx, filter)
			return printDashboardJSON(os.Stdout, items, warnings, grouper)
		}

		tty := isTerminal(os.Stdout)
		live := !dashOnce && tty
		if live {
			enableTerminalEscapes()
		}
		view := dashboardView{grouper: grouper, order: cfg.BranchOrder, filter: filter, links: tty && !dashNoLinks}
		for {
			items, warnings := collector.Refresh(ctx, filter)
			if ctx.Err() != nil {
				return nil
			}
			var b strings.Builder
			view.render(&b, items, warnings, collector.Requests(), time.Now(), live)
			if live {
				fmt.Print("\x1b[H\x1b[2J") // clear the screen and redraw in place
			}
			fmt.Print(b.String())
			if !live {
				return nil
			}
			select {
			case <-ctx.Done():
				fmt.Println()
				return nil
			case <-time.After(dashInterval):
			}
		}
	},
}

// parseWindow accepts Go durations (24h, 90m) and days (3d).
func parseWindow(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid --since %q (use e.g. 24h or 3d)", s)
	}
	return d, nil
}

// dashboardSources builds a source per configured account: GitHub with a token,
// Bitbucket with a token (or a bitbucket_user, for public repositories).
func dashboardSources(cfg *config.Config) ([]dashboard.Source, error) {
	var sources []dashboard.Source
	if token := cfg.GetToken(); token != "" {
		sources = append(sources, &dashboard.GitHubSource{API: newGitHubClient(token)})
	}
	if auth, _ := cfg.ResolveBitbucketAuth(); auth.Token != "" || cfg.BitbucketUser != "" {
		sources = append(sources, &dashboard.BitbucketSource{
			API:        newBitbucketClient(auth.Email, auth.Token),
			User:       cfg.BitbucketUser,
			Workspaces: cfg.BitbucketWorkspaces,
		})
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no account configured: log in with 'gh auth login' (GitHub) and/or set 'prw config set bitbucket_token' (Bitbucket)")
	}
	return sources, nil
}

type dashboardView struct {
	grouper *dashboard.Grouper
	order   []string
	filter  dashboard.Filter
	links   bool
}

func (v dashboardView) render(w io.Writer, items []dashboard.Item, warnings, requests []string, now time.Time, live bool) {
	var targets []string
	var mineItems, reviewItems []dashboard.Item
	for _, it := range items {
		targets = append(targets, it.Target)
		if it.Role == dashboard.RoleReview {
			reviewItems = append(reviewItems, it)
		} else {
			mineItems = append(mineItems, it)
		}
	}
	cols := dashboard.Columns(targets, v.order)

	open := 0
	for _, it := range items {
		if it.State == dashboard.StateOpen {
			open++
		}
	}
	fmt.Fprintf(w, "prw dashboard · %d open PR(s) · %s", open, now.Format("15:04:05"))
	if live {
		fmt.Fprintf(w, " · every %s · Ctrl+C to quit", shortDuration(dashInterval))
	}
	fmt.Fprint(w, "\n\n")

	var sections []dashboard.Section
	if v.filter.Mine || !v.filter.MergedSince.IsZero() {
		title := "Your PRs"
		if !v.filter.MergedSince.IsZero() {
			title += " (incl. merged in the last " + shortDuration(mergedWindow) + ")"
		}
		sections = append(sections, dashboard.Section{Title: title, Rows: v.grouper.Rows(mineItems)})
	}
	if v.filter.Review {
		sections = append(sections, dashboard.Section{Title: "To review", Rows: v.grouper.Rows(reviewItems)})
	}
	width := dashWidth
	if width <= 0 {
		width = terminalWidth()
	}
	dashboard.Render(w, sections, cols, dashboard.RenderOptions{Now: now, Width: width, Links: v.links})

	fmt.Fprintf(w, "\nAPI requests in the last hour: %s\n", strings.Join(requests, " · "))
	const maxWarnings = 5
	for i, warn := range warnings {
		if i == maxWarnings {
			fmt.Fprintf(w, "⚠️  … and %d more\n", len(warnings)-maxWarnings)
			break
		}
		fmt.Fprintf(w, "⚠️  %s\n", warn)
	}
}

// shortDuration formats d without zero parts: "3d", "1h30m", "1m", "30s".
func shortDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	var b strings.Builder
	for _, unit := range []struct {
		size time.Duration
		name string
	}{{time.Hour, "h"}, {time.Minute, "m"}, {time.Second, "s"}} {
		if n := d / unit.size; n > 0 {
			fmt.Fprintf(&b, "%d%s", n, unit.name)
			d -= n * unit.size
		}
	}
	if b.Len() == 0 {
		return "0s"
	}
	return b.String()
}

func printDashboardJSON(w io.Writer, items []dashboard.Item, warnings []string, g *dashboard.Grouper) error {
	type jsonItem struct {
		Task string `json:"task"`
		dashboard.Item
	}
	out := struct {
		Items    []jsonItem `json:"items"`
		Warnings []string   `json:"warnings,omitempty"`
	}{Items: []jsonItem{}, Warnings: warnings}
	for _, it := range items {
		out.Items = append(out.Items, jsonItem{Task: g.Task(it), Item: it})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// isTerminal reports whether f is an interactive terminal (not a pipe or file).
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// terminalWidth is the width of the terminal: COLUMNS, then the console itself,
// then 140.
func terminalWidth() int {
	var n int
	if _, err := fmt.Sscan(os.Getenv("COLUMNS"), &n); err == nil && n > 0 {
		return n
	}
	if n := consoleWidth(); n > 0 {
		return n
	}
	return 140
}

// splitList parses a comma-separated config value.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
