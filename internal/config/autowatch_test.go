package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnableAndDisableAutoWatch(t *testing.T) {
	c := &Config{}
	if !c.EnableAutoWatch(AutoWatchMine) || c.EnableAutoWatch(AutoWatchMine) {
		t.Fatal("EnableAutoWatch must report the first enable only")
	}
	c.EnableAutoWatch(AutoWatchReviewRequested)
	if !c.HasAutoWatch(AutoWatchMine) || !c.HasAutoWatch(AutoWatchReviewRequested) {
		t.Fatalf("AutoWatch = %v", c.AutoWatch)
	}

	c.WatchedPRs = []WatchedPR{
		{Owner: "o", Repo: "r", Number: 1, Source: AutoWatchMine},
		{Owner: "o", Repo: "r", Number: 2},
		{Owner: "o", Repo: "r", Number: 3, Source: AutoWatchReviewRequested},
		{Owner: "o", Repo: "r", Number: 4, Source: AutoWatchMine},
	}
	wasOn, removed := c.DisableAutoWatch(AutoWatchMine)
	if !wasOn || len(removed) != 2 || removed[0].Number != 1 || removed[1].Number != 4 {
		t.Errorf("DisableAutoWatch(mine) = %v, %+v", wasOn, removed)
	}
	if len(c.WatchedPRs) != 2 || c.WatchedPRs[0].Number != 2 || c.WatchedPRs[1].Number != 3 {
		t.Errorf("manual and other-source PRs must be kept: %+v", c.WatchedPRs)
	}
	if c.HasAutoWatch(AutoWatchMine) || !c.HasAutoWatch(AutoWatchReviewRequested) {
		t.Errorf("AutoWatch = %v", c.AutoWatch)
	}

	if wasOn, removed := c.DisableAutoWatch(AutoWatchMine); wasOn || len(removed) != 0 {
		t.Error("disabling twice must be a no-op")
	}
	c.DisableAutoWatch(AutoWatchReviewRequested)
	if c.AutoWatch != nil {
		t.Errorf("AutoWatch should be nil when empty so it's omitted from JSON, got %#v", c.AutoWatch)
	}
}

func TestMarkManual(t *testing.T) {
	c := &Config{WatchedPRs: []WatchedPR{{Owner: "o", Repo: "r", Number: 1, Source: AutoWatchMine}, {Owner: "o", Repo: "r", Number: 2}}}
	if !c.MarkManual("O", "R", 1) || c.WatchedPRs[0].Source != "" {
		t.Errorf("MarkManual should clear the source: %+v", c.WatchedPRs[0])
	}
	if c.MarkManual("o", "r", 1) || c.MarkManual("o", "r", 2) || c.MarkManual("o", "r", 99) {
		t.Error("MarkManual must report false when nothing changes")
	}
}

func TestPRMatchingIsCaseInsensitive(t *testing.T) {
	c := &Config{}
	c.AddPR(WatchedPR{Owner: "DevBlac", Repo: "PRW", Number: 5})
	if c.AddPR(WatchedPR{Owner: "devblac", Repo: "prw", Number: 5}) {
		t.Error("owner/repo differing only in case must not create a duplicate")
	}
	if !c.ApplyPRState(WatchedPR{Owner: "devblac", Repo: "prw", Number: 5, LastKnownState: "success"}) || c.WatchedPRs[0].LastKnownState != "success" {
		t.Error("ApplyPRState must match case-insensitively")
	}
	if !c.RemovePR("DEVBLAC", "prw", 5) || len(c.WatchedPRs) != 0 {
		t.Error("RemovePR must match case-insensitively")
	}
}

func TestAutoWatchFieldsAreOmittedWhenUnused(t *testing.T) {
	data, err := json.Marshal(&Config{WatchedPRs: []WatchedPR{{Owner: "o", Repo: "r", Number: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "auto_watch") || strings.Contains(string(data), "source") {
		t.Errorf("existing configs must not gain new keys: %s", data)
	}
}

func TestApplyPRStateKeepsSource(t *testing.T) {
	c := &Config{WatchedPRs: []WatchedPR{{Owner: "o", Repo: "r", Number: 1, Source: AutoWatchMine}}}
	c.ApplyPRState(WatchedPR{Owner: "o", Repo: "r", Number: 1, LastKnownState: "failure"})
	if c.WatchedPRs[0].Source != AutoWatchMine {
		t.Error("saving polling state must not drop the auto-watch source")
	}
}
