package dashboard

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

var renderNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func open(repo string, number int, source, target string, ci string, ago time.Duration) Item {
	it := item(repo, number, source, target, "Title "+source)
	it.State, it.CI, it.UpdatedAt = StateOpen, ci, renderNow.Add(-ago)
	it.URL = "https://example.com/pr"
	return it
}

func TestStatus(t *testing.T) {
	base := Item{State: StateOpen}
	tests := []struct {
		mutate func(*Item)
		want   string
	}{
		{func(i *Item) { i.State = StateMerged }, "🟣 merged"},
		{func(i *Item) { i.State = StateClosed }, "⚫ closed"},
		{func(i *Item) { i.CI, i.Merge = "failure", "conflict" }, "❌ CI failed"},
		{func(i *Item) { i.CI, i.Merge = "success", "conflict" }, "⛔ conflict"},
		{func(i *Item) { i.CI, i.Review, i.Approvals = "success", "changes_requested", 1 }, "📝 changes ✓1"},
		{func(i *Item) { i.CI, i.CIDone, i.CITotal = "pending", 3, 5 }, "⏳ 3/5"},
		{func(i *Item) { i.CI = "pending" }, "⏳ CI"},
		{func(i *Item) { i.CI, i.Approvals = "success", 2 }, "✅ CI ok ✓2"},
		{func(i *Item) { i.CI, i.Merge, i.Approvals = "success", "ready", 2 }, "🚀 ready ✓2"},
		{func(i *Item) { i.CI = "no_ci" }, "⚪ no CI"},
		{func(i *Item) {}, "…"},
	}
	for _, tt := range tests {
		it := base
		tt.mutate(&it)
		if got := Status(it); got != tt.want {
			t.Errorf("Status(%+v) = %q, want %q", it, got, tt.want)
		}
	}
}

func TestDisplayWidth(t *testing.T) {
	for s, want := range map[string]int{
		"abc":          3,
		"✅ CI ok":      8,
		"⏳ 3/5":        6,
		"#12 🟣 merged": 13,
		"⚠️":           1,
		"\x1b]8;;https://x\x1b\\#12\x1b]8;;\x1b\\": 3,
		"\x1b[31mred\x1b[0m":                       3,
		"—":                                        1,
	} {
		if got := DisplayWidth(s); got != want {
			t.Errorf("DisplayWidth(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestRender(t *testing.T) {
	g, _ := NewGrouper("", "")
	merged := open("sec/issuance-api", 10, "SEC-1234/login", "dev", "", 3*time.Hour)
	merged.State = StateMerged
	pending := open("sec/issuance-api", 11, "cherry-pick/SEC-1234-rc", "rc", "pending", 2*time.Hour)
	pending.CIDone, pending.CITotal, pending.Approvals = 3, 5, 1
	failed := open("sec/wallet", 7, "SEC-1198/fix", "master", "failure", 20*time.Minute)
	items := []Item{merged, pending, failed}
	cols := Columns([]string{"dev", "rc", "master"}, nil)

	var buf bytes.Buffer
	Render(&buf, []Section{{Title: "Your PRs", Rows: g.Rows(items)}}, cols, RenderOptions{Now: renderNow, Width: 120})
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 || lines[0] != "Your PRs (3)" {
		t.Fatalf("output:\n%s", out)
	}
	if !strings.HasPrefix(lines[1], "TASK") || !strings.Contains(lines[1], "dev") || !strings.Contains(lines[1], "master") {
		t.Errorf("header = %q", lines[1])
	}
	// Most recent task first; its PR goes in the master column.
	if !strings.HasPrefix(lines[2], "SEC-1198") || !strings.Contains(lines[2], "#7 ❌ CI failed") || !strings.Contains(lines[2], "20m") {
		t.Errorf("row 1 = %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "SEC-1234") || !strings.Contains(lines[3], "#10 🟣 merged") || !strings.Contains(lines[3], "#11 ⏳ 3/5 ✓1") {
		t.Errorf("row 2 = %q", lines[3])
	}
	// Columns line up: every row is as wide as the header up to the title.
	col := strings.Index(lines[1], "UPDATED")
	for _, l := range lines[2:4] {
		if w := DisplayWidth(l[:strings.LastIndex(l, "  ")]); w < col {
			t.Errorf("misaligned row %q", l)
		}
	}
}

func TestRender_LinksDontBreakAlignment(t *testing.T) {
	g, _ := NewGrouper("", "")
	items := []Item{open("o/r", 1, "A-1/x", "dev", "success", time.Hour), open("o/r", 22, "A-2/y", "dev", "success", 2*time.Hour)}
	var plain, linked bytes.Buffer
	cols := Columns([]string{"dev"}, nil)
	Render(&plain, []Section{{Title: "PRs", Rows: g.Rows(items)}}, cols, RenderOptions{Now: renderNow})
	Render(&linked, []Section{{Title: "PRs", Rows: g.Rows(items)}}, cols, RenderOptions{Now: renderNow, Links: true})
	stripped := escapes.ReplaceAllString(linked.String(), "")
	if stripped != plain.String() {
		t.Errorf("hyperlinks changed the layout:\n%s\nvs\n%s", stripped, plain.String())
	}
	if !strings.Contains(linked.String(), "\x1b]8;;https://example.com/pr\x1b\\#1") {
		t.Error("PR numbers should be hyperlinks")
	}
}

func TestRender_EmptySection(t *testing.T) {
	var buf bytes.Buffer
	Render(&buf, []Section{{Title: "To review"}}, nil, RenderOptions{Now: renderNow})
	if buf.String() != "To review (0)\n  none\n" {
		t.Errorf("output = %q", buf.String())
	}
}

func TestAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second: "30s", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 47 * time.Hour: "47h", 72 * time.Hour: "3d",
	} {
		if got := Ago(renderNow, renderNow.Add(-d)); got != want {
			t.Errorf("Ago(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestRender_OtherColumnNamesTheTarget(t *testing.T) {
	g, _ := NewGrouper("", "")
	stacked := open("o/r", 5, "feat/b", "feat/a", "success", time.Hour)
	cols := Columns([]string{"main", "feat/a"}, nil)
	var buf bytes.Buffer
	Render(&buf, []Section{{Title: "PRs", Rows: g.Rows([]Item{stacked})}}, cols, RenderOptions{Now: renderNow, Width: 160})
	if !strings.Contains(buf.String(), "other") || !strings.Contains(buf.String(), "#5 ✅ CI ok →feat/a") {
		t.Errorf("output:\n%s", buf.String())
	}
}
