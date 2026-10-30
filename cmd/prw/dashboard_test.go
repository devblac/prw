package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/dashboard"
	"github.com/devblac/prw/internal/forge"
)

func TestParseWindow(t *testing.T) {
	for in, want := range map[string]time.Duration{"24h": 24 * time.Hour, "90m": 90 * time.Minute, "3d": 72 * time.Hour, " 1d ": 24 * time.Hour} {
		if got, err := parseWindow(in); err != nil || got != want {
			t.Errorf("parseWindow(%q) = %s, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "0d", "-1h", "soon", "3w"} {
		if _, err := parseWindow(bad); err == nil {
			t.Errorf("parseWindow(%q) should fail", bad)
		}
	}
}

func TestDashboardSources(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("BITBUCKET_TOKEN", "")
	old := config.GHAuthToken
	config.GHAuthToken = func() (string, error) { return "", errNoGH }
	t.Cleanup(func() { config.GHAuthToken = old })

	if _, err := dashboardSources(&config.Config{}); err == nil || !strings.Contains(err.Error(), "no account configured") {
		t.Errorf("err = %v", err)
	}
	names := func(cfg *config.Config) string {
		srcs, err := dashboardSources(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var n []string
		for _, s := range srcs {
			n = append(n, s.Name())
		}
		return strings.Join(n, "+")
	}
	if got := names(&config.Config{GitHubToken: "t"}); got != "GitHub" {
		t.Errorf("GitHub only: %s", got)
	}
	if got := names(&config.Config{BitbucketToken: "t"}); got != "Bitbucket" {
		t.Errorf("Bitbucket token: %s", got)
	}
	if got := names(&config.Config{GitHubToken: "t", BitbucketUser: "{u}"}); got != "GitHub+Bitbucket" {
		t.Errorf("both (public Bitbucket user): %s", got)
	}
}

var errNoGH = &json.UnsupportedValueError{Str: "no gh"}

func dashItem(host, repo string, number int, source, target, state, role, ci string) dashboard.Item {
	owner, name, _ := strings.Cut(repo, "/")
	ref := forge.Ref{Host: host, Owner: owner, Repo: name, Number: number}
	return dashboard.Item{Ref: ref, URL: ref.URL(), Host: host, Repo: repo, Number: number, Title: "T " + source,
		Source: source, Target: target, State: state, Role: role, CI: ci, UpdatedAt: time.Now().Add(-time.Hour)}
}

func TestDashboardView(t *testing.T) {
	g, _ := dashboard.NewGrouper("", "")
	items := []dashboard.Item{
		dashItem(forge.Bitbucket, "sec/api", 1, "SEC-1/a", "dev", dashboard.StateMerged, dashboard.RoleMine, ""),
		dashItem(forge.Bitbucket, "sec/api", 2, "cherry-pick/SEC-1-rc", "rc", dashboard.StateOpen, dashboard.RoleMine, "pending"),
		dashItem(forge.GitHub, "me/tool", 3, "fix/x", "main", dashboard.StateOpen, dashboard.RoleMine, "failure"),
		dashItem(forge.Bitbucket, "sec/web", 4, "SEC-9/z", "dev", dashboard.StateOpen, dashboard.RoleReview, "success"),
	}
	v := dashboardView{grouper: g, filter: dashboard.Filter{Mine: true, Review: true, MergedSince: time.Now().Add(-24 * time.Hour)}}
	oldWindow, oldWidth := mergedWindow, dashWidth
	mergedWindow, dashWidth = 24*time.Hour, 160
	t.Cleanup(func() { mergedWindow, dashWidth = oldWindow, oldWidth })

	var buf bytes.Buffer
	v.render(&buf, items, []string{"Bitbucket: sec: 429"}, []string{"GitHub 5/h", "Bitbucket 12/h"}, time.Now(), true)
	out := buf.String()
	for _, want := range []string{
		"prw dashboard · 3 open PR(s)", "every 1m", "Ctrl+C",
		"Your PRs (incl. merged in the last 1d) (3)",
		"dev", "rc", "main",
		"SEC-1", "#1 🟣 merged", "#2 ⏳ CI",
		"#3 ❌ CI failed",
		"To review (1)", "#4 ✅ CI ok",
		"API requests in the last hour: GitHub 5/h · Bitbucket 12/h",
		"⚠️  Bitbucket: sec: 429",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The task and its cherry-pick share a row.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "#1 🟣 merged") && !strings.Contains(line, "#2 ⏳") {
			t.Errorf("SEC-1 and its cherry-pick must share a row: %q", line)
		}
	}
}

func TestPrintDashboardJSON(t *testing.T) {
	g, _ := dashboard.NewGrouper("", "")
	var buf bytes.Buffer
	items := []dashboard.Item{dashItem(forge.Bitbucket, "sec/api", 2, "cherry-pick/SEC-1-rc", "rc", dashboard.StateOpen, dashboard.RoleMine, "success")}
	if err := printDashboardJSON(&buf, items, nil, g); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	it := out.Items[0]
	if it["task"] != "SEC-1" || it["url"] != "https://bitbucket.org/sec/api/pull-requests/2" || it["target_branch"] != "rc" || it["ci"] != "success" {
		t.Errorf("item = %v", it)
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{24 * time.Hour: "1d", 72 * time.Hour: "3d", time.Minute: "1m", 90 * time.Minute: "1h30m", 30 * time.Second: "30s"} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestSplitList(t *testing.T) {
	if got := strings.Join(splitList(" dev, qa ,,main "), "|"); got != "dev|qa|main" {
		t.Errorf("splitList = %s", got)
	}
}
