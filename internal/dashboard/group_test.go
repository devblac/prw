package dashboard

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func item(repo string, number int, source, target, title string) Item {
	return Item{Repo: repo, Number: number, Source: source, Target: target, Title: title}
}

func TestTask_Auto(t *testing.T) {
	g, err := NewGrouper("", "")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		it   Item
		want string
	}{
		// Issue key in the branch (as seen on Bitbucket: ONECLOUD-13509/operate-without-descriptor).
		{item("a/api", 1, "ONECLOUD-13509/operate-without-descriptor", "master", "ACE can operate"), "ONECLOUD-13509"},
		{item("a/api", 2, "feature/SEC-1234-login", "dev", "Login"), "SEC-1234"},
		{item("a/api", 3, "cherry-pick/SEC-1234-rc", "rc", "Login (rc)"), "SEC-1234"},
		// Key only in the title.
		{item("a/api", 4, "fix-timeout", "dev", "[PAY-77] Fix timeout"), "PAY-77"},
		// The branch's key wins over others mentioned in the title.
		{item("a/api", 5, "SEC-9/x", "main", "Revert SEC-8"), "SEC-9"},
		// No key: the branch without cherry-pick decorations.
		{item("a/api", 6, "feature/login", "dev", "Login"), "feature/login"},
		{item("a/api", 7, "cherry-pick/feature/login-rc", "rc", "Login"), "feature/login"},
		{item("a/api", 8, "feature/login-to-master", "master", "Login"), "feature/login"},
		{item("a/api", 9, "backport-feature/login-main", "main", "Login"), "feature/login"},
		{item("a/api", 10, "renovate/node-24.x", "master", "Update Node.js"), "renovate/node-24.x"},
	}
	for _, tt := range tests {
		if got := g.Task(tt.it); got != tt.want {
			t.Errorf("Task(%q, %q) = %q, want %q", tt.it.Source, tt.it.Title, got, tt.want)
		}
	}
}

func TestTask_Modes(t *testing.T) {
	it := item("a/api", 3, "cherry-pick/SEC-1234-rc", "rc", "Login")
	for mode, want := range map[string]string{
		GroupIssue: "SEC-1234", GroupBranch: "sec-1234", GroupTitle: "Login", GroupNone: "#3",
	} {
		g, _ := NewGrouper(mode, "")
		if got := g.Task(it); got != want {
			t.Errorf("%s: %q, want %q", mode, got, want)
		}
	}
	g, _ := NewGrouper(GroupIssue, "")
	if got := g.Task(item("a/api", 4, "feature/login", "dev", "Login")); got != "#4" {
		t.Errorf("issue mode without a key = %q, want the PR alone", got)
	}
	if _, err := NewGrouper("bogus", ""); err == nil {
		t.Error("invalid mode must fail")
	}
}

func TestTask_CustomIssuePattern(t *testing.T) {
	// e.g. a team using GitHub issue numbers in branches: "123-fix-login".
	g, err := NewGrouper(GroupAuto, `^[0-9]+`)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Task(item("a/api", 1, "123-fix-login", "dev", "Fix")); got != "123" {
		t.Errorf("custom pattern: %q", got)
	}
	if _, err := NewGrouper(GroupAuto, `[`); err == nil {
		t.Error("an invalid pattern must fail")
	}
}

func TestRows_GroupsTasksAcrossBranchesAndRepos(t *testing.T) {
	g, _ := NewGrouper("", "")
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	at := func(it Item, ago time.Duration) Item { it.UpdatedAt = now.Add(-ago); return it }
	rows := g.Rows([]Item{
		at(item("sec/api", 1, "SEC-1/a", "dev", ""), 3*time.Hour),
		at(item("sec/api", 2, "cherry-pick/SEC-1-rc", "rc", ""), 2*time.Hour),
		at(item("sec/web", 3, "SEC-1/a", "dev", ""), 4*time.Hour),
		at(item("sec/api", 4, "SEC-2/b", "dev", ""), time.Hour), // most recent task first
		at(item("sec/api", 5, "SEC-1-master", "master", ""), 5*time.Hour),
	})
	var got []string
	for _, r := range rows {
		got = append(got, r.Task+" "+r.Repo+" "+itemNumbers(r.Items))
	}
	want := "SEC-2 sec/api 4|SEC-1 sec/api 1,2,5|SEC-1 sec/web 3"
	if strings.Join(got, "|") != want {
		t.Errorf("rows = %s\nwant   %s", strings.Join(got, "|"), want)
	}
}

func itemNumbers(items []Item) string {
	var s []string
	for _, it := range items {
		s = append(s, strconv.Itoa(it.Number))
	}
	return strings.Join(s, ",")
}

func TestColumns_TiersShareColumns(t *testing.T) {
	cols := Columns([]string{"master", "dev", "rc", "main", "release/1.2", "feature-x", "develop", "release"}, nil)
	var titles []string
	for _, c := range cols {
		titles = append(titles, c.Title)
	}
	if got := strings.Join(titles, " "); got != "dev|develop rc|release|release/* main|master other" {
		t.Errorf("columns = %s", got)
	}
	if ColumnOf(cols, "release/1.2") != 1 || ColumnOf(cols, "main") != 2 || ColumnOf(cols, "nope") != -1 {
		t.Error("ColumnOf")
	}
}

func TestColumns_YourTwoJobs(t *testing.T) {
	// Job 1: dev → rc → master. Job 2: dev → release → master or main.
	for _, tc := range []struct {
		targets []string
		want    string
	}{
		{[]string{"master", "rc", "dev"}, "dev rc master"},
		{[]string{"main", "release", "dev"}, "dev release main"},
	} {
		var titles []string
		for _, c := range Columns(tc.targets, nil) {
			titles = append(titles, c.Title)
		}
		if got := strings.Join(titles, " "); got != tc.want {
			t.Errorf("%v → %s, want %s", tc.targets, got, tc.want)
		}
	}
}

func TestColumns_CustomOrder(t *testing.T) {
	var titles []string
	for _, c := range Columns([]string{"main", "uat", "qa", "dev", "hotfix"}, []string{"dev", "qa", "uat", "main"}) {
		titles = append(titles, c.Title)
	}
	if got := strings.Join(titles, " "); got != "dev qa uat main other" {
		t.Errorf("columns = %s", got)
	}
}
