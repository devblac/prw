package dashboard

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Grouping modes (--group-by / group_by).
const (
	GroupAuto   = "auto"   // issue key, else branch
	GroupIssue  = "issue"  // issue key only; PRs without one are grouped alone
	GroupBranch = "branch" // source branch without cherry-pick decorations
	GroupTitle  = "title"
	GroupNone   = "none" // one row per PR
)

// DefaultIssuePattern matches issue keys like PROJ-123 (Jira, Linear, YouTrack,
// Shortcut's sc-123 is matched case-insensitively by the branch fallback instead).
const DefaultIssuePattern = `\b[A-Z][A-Z0-9]{1,9}-[0-9]+\b`

// ValidGroupBy reports whether mode is a grouping mode.
func ValidGroupBy(mode string) bool {
	switch mode {
	case GroupAuto, GroupIssue, GroupBranch, GroupTitle, GroupNone:
		return true
	}
	return false
}

// Grouper assigns items to tasks.
type Grouper struct {
	Mode  string
	Issue *regexp.Regexp
}

// NewGrouper compiles pattern (DefaultIssuePattern when empty).
func NewGrouper(mode, pattern string) (*Grouper, error) {
	if mode == "" {
		mode = GroupAuto
	}
	if !ValidGroupBy(mode) {
		return nil, fmt.Errorf("invalid grouping %q (expected auto, issue, branch, title or none)", mode)
	}
	if pattern == "" {
		pattern = DefaultIssuePattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid issue_pattern: %w", err)
	}
	return &Grouper{Mode: mode, Issue: re}, nil
}

// Task returns the task an item belongs to.
func (g *Grouper) Task(it Item) string {
	switch g.Mode {
	case GroupNone:
		return fmt.Sprintf("#%d", it.Number)
	case GroupTitle:
		return strings.TrimSpace(it.Title)
	case GroupBranch:
		return BranchBase(it.Source, it.Target)
	}
	// The branch first: titles of cherry-picks sometimes mention other keys.
	for _, s := range []string{it.Source, it.Title} {
		if k := g.Issue.FindString(s); k != "" {
			return strings.ToUpper(k)
		}
	}
	if g.Mode == GroupIssue {
		return fmt.Sprintf("#%d", it.Number)
	}
	return BranchBase(it.Source, it.Target)
}

var (
	cherryPrefix = regexp.MustCompile(`(?i)^(cherry[-_]?pick(ed)?|backport|cp)[-_/]+`)
	// cherrySuffix strips "-to-rc", "-rc", "_master", "-cherry-pick", "-backport-2",
	// a trailing short hash or number, repeatedly.
	cherrySuffix = regexp.MustCompile(`(?i)[-_/](to[-_])?(dev|develop|development|staging|stage|qa|uat|rc|release|main|master|prod|production|cherry[-_]?pick(ed)?|backport|cp|[0-9a-f]{7,40}|[0-9]{1,3})$`)
)

// BranchBase is a source branch without cherry-pick / backport decorations, so a
// branch and its cherry-picks to other targets share a task:
// "cherry-pick/feature/login-rc" and "feature/login" → "feature/login".
func BranchBase(source, target string) string {
	b := strings.TrimSpace(source)
	for {
		stripped := cherryPrefix.ReplaceAllString(b, "")
		if stripped == b {
			break
		}
		b = stripped
	}
	if target != "" {
		for _, sep := range []string{"-to-", "_to_", "-", "_", "/"} {
			b = strings.TrimSuffix(b, sep+target)
		}
	}
	for i := 0; i < 5; i++ {
		stripped := cherrySuffix.ReplaceAllString(b, "")
		if stripped == b || stripped == "" {
			break
		}
		b = stripped
	}
	return strings.ToLower(b)
}

// Row is one line of the dashboard: a task in a repository.
type Row struct {
	Task  string
	Repo  string
	Items []Item
	// Updated is the most recent update of its items.
	Updated time.Time
}

// Rows groups items by task and repository. Rows are ordered by task, the tasks
// with the most recent activity first; rows of the same task by repository.
func (g *Grouper) Rows(items []Item) []Row {
	type key struct{ task, repo string }
	index := map[key]int{}
	var rows []Row
	for _, it := range items {
		k := key{g.Task(it), it.Repo}
		i, ok := index[k]
		if !ok {
			i = len(rows)
			index[k] = i
			rows = append(rows, Row{Task: k.task, Repo: k.repo})
		}
		rows[i].Items = append(rows[i].Items, it)
		if it.UpdatedAt.After(rows[i].Updated) {
			rows[i].Updated = it.UpdatedAt
		}
	}
	taskUpdated := map[string]time.Time{}
	for _, r := range rows {
		if r.Updated.After(taskUpdated[r.Task]) {
			taskUpdated[r.Task] = r.Updated
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Task != b.Task {
			if !taskUpdated[a.Task].Equal(taskUpdated[b.Task]) {
				return taskUpdated[a.Task].After(taskUpdated[b.Task])
			}
			return a.Task < b.Task
		}
		return a.Repo < b.Repo
	})
	return rows
}
