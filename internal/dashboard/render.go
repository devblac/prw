package dashboard

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Width limits of the task name and of a target branch named in the "other" column.
const (
	maxTaskWidth   = 40
	maxOtherTarget = 24
)

// Section is one table of the dashboard ("Your PRs", "To review").
type Section struct {
	Title string
	Rows  []Row
}

// RenderOptions controls how the dashboard is drawn.
type RenderOptions struct {
	Now   time.Time
	Width int // terminal width; the title column is cut to fit
	// Links turns PR numbers into terminal hyperlinks (OSC 8), which most modern
	// terminals make clickable.
	Links bool
}

// Render draws the sections as tables with one column per target branch.
func Render(w io.Writer, sections []Section, cols []Column, opts RenderOptions) {
	if opts.Width <= 0 {
		opts.Width = 140
	}
	for i, s := range sections {
		if i > 0 {
			fmt.Fprintln(w)
		}
		renderSection(w, s, cols, opts)
	}
}

func renderSection(w io.Writer, s Section, cols []Column, opts RenderOptions) {
	fmt.Fprintf(w, "%s (%d)\n", s.Title, countItems(s.Rows))
	if len(s.Rows) == 0 {
		fmt.Fprintln(w, "  none")
		return
	}

	header := []string{"TASK", "REPO"}
	for _, c := range cols {
		header = append(header, c.Title)
	}
	header = append(header, "UPDATED", "TITLE")

	table := [][]string{header}
	prevTask := ""
	for _, r := range s.Rows {
		line := make([]string, 0, len(header))
		task := truncate(r.Task, maxTaskWidth)
		if r.Task == prevTask {
			task = "" // the task is named on its first row only
		}
		prevTask = r.Task
		line = append(line, task, shortRepo(r.Repo))
		cells := make([][]string, len(cols))
		var title string
		var latest time.Time
		for _, it := range r.Items {
			if c := ColumnOf(cols, it.Target); c >= 0 {
				cell := Cell(it, opts.Links)
				if cols[c].Other {
					cell += " →" + truncate(it.Target, maxOtherTarget)
				}
				cells[c] = append(cells[c], cell)
			}
			if it.UpdatedAt.After(latest) || title == "" {
				latest, title = it.UpdatedAt, it.Title
			}
		}
		for _, c := range cells {
			if len(c) == 0 {
				line = append(line, "—")
			} else {
				line = append(line, strings.Join(c, "  "))
			}
		}
		line = append(line, Ago(opts.Now, r.Updated), title)
		table = append(table, line)
	}
	writeTable(w, table, opts.Width)
}

func countItems(rows []Row) int {
	n := 0
	for _, r := range rows {
		n += len(r.Items)
	}
	return n
}

// shortRepo drops the owner / workspace when it adds nothing: most people's PRs
// live in one or two. Kept as is otherwise.
func shortRepo(repo string) string {
	return repo
}

// Cell is one PR in a branch column: its number and most important state.
func Cell(it Item, links bool) string {
	num := fmt.Sprintf("#%d", it.Number)
	if links && it.URL != "" {
		num = "\x1b]8;;" + it.URL + "\x1b\\" + num + "\x1b]8;;\x1b\\"
	}
	return num + " " + Status(it)
}

// Status summarizes a PR in a few characters, the most pressing thing first:
// failed CI, conflict, changes requested, running CI, then green.
func Status(it Item) string {
	switch it.State {
	case StateMerged:
		return "🟣 merged"
	case StateClosed:
		return "⚫ closed"
	}
	approvals := ""
	if it.Approvals > 0 {
		approvals = fmt.Sprintf(" ✓%d", it.Approvals)
	}
	switch {
	case it.CI == "failure" || it.CI == "error":
		return "❌ CI failed" + approvals
	case it.Merge == "conflict":
		return "⛔ conflict" + approvals
	case it.Review == "changes_requested":
		return "📝 changes" + approvals
	case it.CI == "pending":
		if it.CITotal > 0 {
			return fmt.Sprintf("⏳ %d/%d", it.CIDone, it.CITotal) + approvals
		}
		return "⏳ CI" + approvals
	case it.CI == "no_ci":
		if it.Merge == "ready" {
			return "🚀 ready" + approvals
		}
		return "⚪ no CI" + approvals
	case it.CI == "success":
		if it.Merge == "ready" {
			return "🚀 ready" + approvals
		}
		return "✅ CI ok" + approvals
	}
	return "…" + approvals // not loaded yet
}

// Ago is a compact age: "45s", "12m", "3h", "5d".
func Ago(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// writeTable pads columns by their visible width; the last column (the title) is
// cut so lines fit width.
func writeTable(w io.Writer, table [][]string, width int) {
	ncol := len(table[0])
	widths := make([]int, ncol)
	for _, row := range table {
		for i, cell := range row[:ncol-1] {
			if n := DisplayWidth(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}
	used := 0
	for _, n := range widths[:ncol-1] {
		used += n + 2
	}
	titleWidth := width - used
	for _, row := range table {
		var b strings.Builder
		for i, cell := range row[:ncol-1] {
			b.WriteString(cell)
			b.WriteString(strings.Repeat(" ", widths[i]-DisplayWidth(cell)+2))
		}
		if titleWidth >= 10 {
			b.WriteString(truncate(row[ncol-1], titleWidth))
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

var escapes = regexp.MustCompile("\x1b\\]8;;[^\x1b]*\x1b\\\\|\x1b\\[[0-9;]*[A-Za-z]")

// DisplayWidth is how many terminal columns s takes: escape sequences take none,
// emoji take two.
func DisplayWidth(s string) int {
	s = escapes.ReplaceAllString(s, "")
	n := 0
	for _, r := range s {
		switch {
		case r == 0xFE0F || r == 0x200D:
			// variation selector / zero-width joiner
		case r >= 0x1F000, r == 0x2705, r == 0x274C, r == 0x23F3, r == 0x231B,
			r == 0x26AA, r == 0x26AB, r == 0x26D4, r == 0x2B50:
			n += 2
		default:
			n++
		}
	}
	return n
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}
