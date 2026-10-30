package dashboard

import (
	"sort"
	"strings"
)

// branchTiers ranks well-known target branches in promotion order. Branches in the
// same tier share a column.
var branchTiers = [][]string{
	{"dev", "develop", "development", "devel"},
	{"staging", "stage", "qa", "test", "testing", "uat", "preprod", "pre-prod"},
	{"rc", "release", "release-candidate", "pre-release", "prerelease"},
	{"main", "master", "trunk", "prod", "production", "live", "stable"},
}

// tierOf returns the promotion tier of a branch, or -1. "release/1.2" and
// "release-1.2" count as release.
func tierOf(branch string) int {
	b := strings.ToLower(branch)
	for i, tier := range branchTiers {
		for _, name := range tier {
			if b == name || strings.HasPrefix(b, name+"/") || strings.HasPrefix(b, name+"-") {
				return i
			}
		}
	}
	return -1
}

// Column is one target-branch column of the dashboard.
type Column struct {
	Title    string   // e.g. "main|master"
	Branches []string // the target branches shown in it
	// Other holds every remaining target branch (e.g. the feature branches stacked
	// PRs point at), so they don't each add a column; its cells name the target.
	Other bool
}

// OtherTitle is the title of the column for target branches outside the order.
const OtherTitle = "other"

// Columns derives the dashboard's columns from the target branches in use.
//
// With order (branch_order), each listed branch is a column in that order. Otherwise
// branches of the same promotion tier (dev, staging, rc/release, main/master) share
// a column, tiers in promotion order. Any other branch goes in one last "other"
// column.
func Columns(targets []string, order []string) []Column {
	seen := map[string]bool{}
	var branches []string
	for _, t := range targets {
		if t != "" && !seen[t] {
			seen[t] = true
			branches = append(branches, t)
		}
	}
	sort.Strings(branches)

	if len(order) > 0 {
		rank := map[string]int{}
		for i, b := range order {
			rank[strings.ToLower(strings.TrimSpace(b))] = i + 1
		}
		sort.SliceStable(branches, func(i, j int) bool {
			return rank[strings.ToLower(branches[i])] < rank[strings.ToLower(branches[j])]
		})
		var cols []Column
		var others []string
		for _, b := range branches {
			if rank[strings.ToLower(b)] == 0 {
				others = append(others, b)
				continue
			}
			cols = append(cols, Column{Title: b, Branches: []string{b}})
		}
		return withOther(cols, others)
	}

	tiers := make([][]string, len(branchTiers))
	var others []string
	for _, b := range branches {
		if t := tierOf(b); t >= 0 {
			tiers[t] = append(tiers[t], b)
		} else {
			others = append(others, b)
		}
	}
	var cols []Column
	for _, bs := range tiers {
		if len(bs) > 0 {
			cols = append(cols, Column{Title: tierTitle(bs), Branches: bs})
		}
	}
	return withOther(cols, others)
}

func withOther(cols []Column, others []string) []Column {
	if len(others) > 0 {
		cols = append(cols, Column{Title: OtherTitle, Branches: others, Other: true})
	}
	return cols
}

// tierTitle names a shared column: "main|master", or "release/*" for versioned
// release branches.
func tierTitle(branches []string) string {
	var names []string
	seen := map[string]bool{}
	for _, b := range branches {
		name := b
		if i := strings.IndexAny(b, "/"); i > 0 {
			name = b[:i] + "/*"
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) > 3 {
		names = append(names[:3], "…")
	}
	return strings.Join(names, "|")
}

// ColumnOf returns the index of the column showing target, or -1.
func ColumnOf(cols []Column, target string) int {
	for i, c := range cols {
		for _, b := range c.Branches {
			if b == target {
				return i
			}
		}
	}
	return -1
}
