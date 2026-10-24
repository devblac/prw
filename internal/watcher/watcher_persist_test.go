package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devblac/prw/internal/config"
	"github.com/devblac/prw/internal/github"
)

func useTempConfig(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".prw", "config.json")
	old := config.ConfigPath
	config.ConfigPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { config.ConfigPath = old })
}

func newPR(number int, sha string) *github.PullRequest {
	pr := &github.PullRequest{Number: number, Title: "PR"}
	pr.Head.SHA = sha
	return pr
}

func watched(number int, state string) config.WatchedPR {
	return config.WatchedPR{Owner: "owner", Repo: "repo", Number: number, LastKnownState: state}
}

func TestWatcherReloadsWatchListEachTick(t *testing.T) {
	useTempConfig(t)

	// `prw run` started with only PR #1 being watched.
	onDisk := &config.Config{PollIntervalSeconds: 20, WatchedPRs: []config.WatchedPR{watched(1, "pending")}}
	if err := onDisk.Save(); err != nil {
		t.Fatal(err)
	}
	inMemory := &config.Config{PollIntervalSeconds: 20, WatchedPRs: []config.WatchedPR{watched(1, "pending")}}

	client := &mockGitHubClient{
		prs: map[string]*github.PullRequest{
			"owner/repo/1": newPR(1, "sha1"),
			"owner/repo/2": newPR(2, "sha2"),
		},
		statuses: map[string]*github.CombinedStatus{
			"sha1": {State: "success", SHA: "sha1"},
			"sha2": {State: "failure", SHA: "sha2"},
		},
	}
	w := New(client, inMemory, &mockNotifier{})

	// In another terminal: `prw unwatch` #1 and `prw watch` #2.
	if err := config.Update(func(c *config.Config) error {
		c.RemovePR("owner", "repo", 1)
		c.AddPR(watched(2, ""))
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	w.reloadWatchedPRs()
	if len(w.config.WatchedPRs) != 1 || w.config.WatchedPRs[0].Number != 2 {
		t.Fatalf("watcher did not pick up the new watch list: %+v", w.config.WatchedPRs)
	}

	w.checkAllPRs(context.Background())

	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.WatchedPRs) != 1 || loaded.WatchedPRs[0].Number != 2 {
		t.Fatalf("saving state changed the watch list: %+v", loaded.WatchedPRs)
	}
	if loaded.WatchedPRs[0].LastKnownState != "failure" || loaded.WatchedPRs[0].LastKnownSHA != "sha2" {
		t.Errorf("state of the newly added PR was not persisted: %+v", loaded.WatchedPRs[0])
	}
}

func TestWatcherDoesNotResurrectUnwatchedPR(t *testing.T) {
	useTempConfig(t)

	onDisk := &config.Config{PollIntervalSeconds: 20, WatchedPRs: []config.WatchedPR{watched(1, "pending"), watched(2, "pending")}}
	if err := onDisk.Save(); err != nil {
		t.Fatal(err)
	}
	inMemory := &config.Config{PollIntervalSeconds: 20, WatchedPRs: []config.WatchedPR{watched(1, "pending"), watched(2, "pending")}}

	client := &mockGitHubClient{
		prs: map[string]*github.PullRequest{
			"owner/repo/1": newPR(1, "sha1"),
			"owner/repo/2": newPR(2, "sha2"),
		},
		statuses: map[string]*github.CombinedStatus{
			"sha1": {State: "success", SHA: "sha1"},
			"sha2": {State: "success", SHA: "sha2"},
		},
	}
	w := New(client, inMemory, &mockNotifier{})

	// The user unwatches #2 while a poll is in flight (after the watcher's last reload).
	if err := config.Update(func(c *config.Config) error {
		c.RemovePR("owner", "repo", 2)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	w.checkAllPRs(context.Background())

	loaded, _ := config.Load()
	if len(loaded.WatchedPRs) != 1 || loaded.WatchedPRs[0].Number != 1 {
		t.Fatalf("unwatched PR was resurrected or PR #1 lost: %+v", loaded.WatchedPRs)
	}
	if loaded.WatchedPRs[0].LastKnownState != "success" {
		t.Errorf("PR #1 state not persisted: %+v", loaded.WatchedPRs[0])
	}
}

func TestWatcherKeepsPRAddedDuringPoll(t *testing.T) {
	useTempConfig(t)

	onDisk := &config.Config{PollIntervalSeconds: 20, WatchedPRs: []config.WatchedPR{watched(1, "pending")}}
	if err := onDisk.Save(); err != nil {
		t.Fatal(err)
	}
	inMemory := &config.Config{PollIntervalSeconds: 20, WatchedPRs: []config.WatchedPR{watched(1, "pending")}}

	client := &mockGitHubClient{
		prs:      map[string]*github.PullRequest{"owner/repo/1": newPR(1, "sha1")},
		statuses: map[string]*github.CombinedStatus{"sha1": {State: "success", SHA: "sha1"}},
	}
	w := New(client, inMemory, &mockNotifier{})

	// `prw watch` adds #7 while the watcher is busy; the old code overwrote the file
	// with its stale in-memory list at the end of the poll and lost it.
	if err := config.Update(func(c *config.Config) error {
		c.AddPR(watched(7, ""))
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	w.checkAllPRs(context.Background())

	loaded, _ := config.Load()
	if len(loaded.WatchedPRs) != 2 {
		t.Fatalf("PR added during the poll was lost: %+v", loaded.WatchedPRs)
	}
}

func TestWatcherDoesNotPersistRunOnlyOverrides(t *testing.T) {
	useTempConfig(t)

	onDisk := &config.Config{
		PollIntervalSeconds: 20,
		NotificationFilter:  config.NotificationFilterChange,
		WatchedPRs:          []config.WatchedPR{watched(1, "pending")},
	}
	if err := onDisk.Save(); err != nil {
		t.Fatal(err)
	}

	// `prw run --on fail` overrides the filter in memory only.
	inMemory := &config.Config{
		PollIntervalSeconds: 5,
		NotificationFilter:  config.NotificationFilterFail,
		WatchedPRs:          []config.WatchedPR{watched(1, "pending")},
	}
	client := &mockGitHubClient{
		prs:      map[string]*github.PullRequest{"owner/repo/1": newPR(1, "sha1")},
		statuses: map[string]*github.CombinedStatus{"sha1": {State: "success", SHA: "sha1"}},
	}
	New(client, inMemory, &mockNotifier{}).checkAllPRs(context.Background())

	loaded, _ := config.Load()
	if loaded.NotificationFilter != config.NotificationFilterChange {
		t.Errorf("--on override leaked into the config file: filter = %q", loaded.NotificationFilter)
	}
	if loaded.PollIntervalSeconds != 20 {
		t.Errorf("in-memory settings leaked into the config file: poll = %d", loaded.PollIntervalSeconds)
	}
}

func TestWatcherReloadKeepsListOnLoadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config-is-a-dir")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	old := config.ConfigPath
	config.ConfigPath = func() (string, error) { return path, nil }
	defer func() { config.ConfigPath = old }()

	inMemory := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, "pending")}}
	w := New(&mockGitHubClient{}, inMemory, &mockNotifier{})

	w.reloadWatchedPRs()

	if len(w.config.WatchedPRs) != 1 {
		t.Errorf("a config read error must not empty the watch list: %+v", w.config.WatchedPRs)
	}
}

func TestCheckAllPRs_StoppedMidRoundKeepsEveryPR(t *testing.T) {
	useTempConfig(t)
	cfg := &config.Config{WatchedPRs: []config.WatchedPR{watched(1, "success"), watched(2, "failure")}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Ctrl-C: every request now fails with context.Canceled
	client := &mockGitHubClient{err: ctx.Err()}
	notifier := &mockNotifier{}
	w := New(client, cfg, notifier)

	w.checkAllPRs(ctx)

	loaded, _ := config.Load()
	if len(loaded.WatchedPRs) != 2 || loaded.WatchedPRs[0].LastKnownState != "success" || loaded.WatchedPRs[1].LastKnownState != "failure" {
		t.Errorf("watch list = %+v, want both PRs unchanged", loaded.WatchedPRs)
	}
	if len(notifier.events) != 0 {
		t.Errorf("events = %+v", notifier.events)
	}
}
