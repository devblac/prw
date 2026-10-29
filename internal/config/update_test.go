package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// useTempConfig points ConfigPath at a fresh temp dir and returns the config file path.
func useTempConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".prw", "config.json")
	old := ConfigPath
	ConfigPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { ConfigPath = old })
	return path
}

// runtimeSupportsUnixPerms reports whether file mode bits are meaningful on this OS.
func runtimeSupportsUnixPerms() bool {
	return runtime.GOOS != "windows"
}

func pr(number int) WatchedPR {
	return WatchedPR{Owner: "owner", Repo: "repo", Number: number}
}

func TestSave_LeavesNoTempFilesAndKeepsPermissions(t *testing.T) {
	path := useTempConfig(t)

	cfg := DefaultConfig()
	for i := 0; i < 3; i++ {
		if err := cfg.Save(); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Errorf("unexpected leftover file after Save: %s", e.Name())
		}
	}
	if runtimeSupportsUnixPerms() {
		info, _ := os.Stat(path)
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("config permissions = %o, want 600 (it can hold a token)", perm)
		}
	}
}

func TestSave_ReplacesExistingFile(t *testing.T) {
	useTempConfig(t)

	first := DefaultConfig()
	first.WebhookURL = "https://first.example.com"
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}
	second := DefaultConfig()
	second.WebhookURL = "https://second.example.com"
	if err := second.Save(); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WebhookURL != "https://second.example.com" {
		t.Errorf("expected second save to win, got %q", loaded.WebhookURL)
	}
}

func TestUpdate_AppliesAndSaves(t *testing.T) {
	useTempConfig(t)

	if err := Update(func(c *Config) error {
		c.AddPR(pr(1))
		return nil
	}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	loaded, _ := Load()
	if len(loaded.WatchedPRs) != 1 || loaded.WatchedPRs[0].Number != 1 {
		t.Errorf("update was not persisted: %+v", loaded.WatchedPRs)
	}
}

func TestUpdate_ErrNoChangesSkipsSave(t *testing.T) {
	path := useTempConfig(t)

	if err := Update(func(c *Config) error { return ErrNoChanges }); err != nil {
		t.Fatalf("ErrNoChanges should not surface as an error, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("config file must not be created when nothing changed (stat err=%v)", err)
	}
}

func TestUpdate_CallbackErrorIsReturnedAndNotSaved(t *testing.T) {
	path := useTempConfig(t)

	boom := errors.New("boom")
	if err := Update(func(c *Config) error {
		c.AddPR(pr(1))
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("expected callback error, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a failed update must not be saved")
	}
}

func TestUpdate_ReleasesLock(t *testing.T) {
	path := useTempConfig(t)

	if err := Update(func(c *Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock file should be removed after Update (stat err=%v)", err)
	}

	// The lock is also released when the callback fails.
	_ = Update(func(c *Config) error { return errors.New("boom") })
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock file should be removed after a failed Update (stat err=%v)", err)
	}
}

func TestUpdate_ConfigPathError(t *testing.T) {
	old := ConfigPath
	defer func() { ConfigPath = old }()
	ConfigPath = func() (string, error) { return "", errors.New("no home") }

	if err := Update(func(c *Config) error { return nil }); err == nil {
		t.Error("expected error when the config path cannot be determined")
	}
}

func TestUpdate_ConcurrentWritersLoseNothing(t *testing.T) {
	useTempConfig(t)

	const writers = 12
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 1; i <= writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			errs <- Update(func(c *Config) error {
				c.AddPR(pr(n))
				return nil
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Update failed: %v", err)
		}
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.WatchedPRs) != writers {
		t.Errorf("lost updates: got %d PRs, want %d", len(loaded.WatchedPRs), writers)
	}
}

func TestAcquireLock_TimesOutWhenHeld(t *testing.T) {
	path := useTempConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".lock", nil, 0600); err != nil {
		t.Fatal(err)
	}

	oldWait := lockWait
	lockWait = 100 * time.Millisecond
	defer func() { lockWait = oldWait }()

	err := Update(func(c *Config) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for config lock") {
		t.Errorf("expected lock timeout error, got %v", err)
	}
	if _, statErr := os.Stat(path + ".lock"); statErr != nil {
		t.Error("a lock held by someone else must not be removed")
	}
}

func TestAcquireLock_RemovesStaleLock(t *testing.T) {
	path := useTempConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * lockStale)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}

	if err := Update(func(c *Config) error {
		c.AddPR(pr(1))
		return nil
	}); err != nil {
		t.Fatalf("a stale lock from a crashed process should be ignored, got %v", err)
	}
}

func TestApplyPRState(t *testing.T) {
	cfg := &Config{WatchedPRs: []WatchedPR{{Owner: "o", Repo: "r", Number: 1, Title: "Old"}}}
	checked := time.Now()

	ok := cfg.ApplyPRState(WatchedPR{Owner: "o", Repo: "r", Number: 1, LastKnownSHA: "sha", LastKnownState: "success", LastChecked: checked})
	if !ok {
		t.Fatal("expected PR to be found")
	}
	got := cfg.WatchedPRs[0]
	if got.LastKnownSHA != "sha" || got.LastKnownState != "success" || !got.LastChecked.Equal(checked) {
		t.Errorf("state not applied: %+v", got)
	}
	if got.Title != "Old" {
		t.Errorf("an empty title must not erase the stored one, got %q", got.Title)
	}

	cfg.ApplyPRState(WatchedPR{Owner: "o", Repo: "r", Number: 1, Title: "New"})
	if cfg.WatchedPRs[0].Title != "New" {
		t.Errorf("title not updated, got %q", cfg.WatchedPRs[0].Title)
	}

	if cfg.ApplyPRState(WatchedPR{Owner: "o", Repo: "r", Number: 99}) {
		t.Error("unknown PR must report false")
	}
}

func TestSavePRStates_PreservesConcurrentChanges(t *testing.T) {
	useTempConfig(t)

	// What a long-running command loaded at the start.
	snapshot := []WatchedPR{pr(1), pr(2)}
	initial := &Config{PollIntervalSeconds: 20, WatchedPRs: append([]WatchedPR(nil), snapshot...)}
	if err := initial.Save(); err != nil {
		t.Fatal(err)
	}

	// Meanwhile, other commands remove #2, add #3 and change a setting.
	if err := Update(func(c *Config) error {
		c.RemovePR("", "owner", "repo", 2)
		c.AddPR(pr(3))
		c.WebhookURL = "https://hooks.example.com/x"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The long-running command finishes and persists its results.
	snapshot[0].LastKnownState = "failure"
	snapshot[1].LastKnownState = "success"
	if err := SavePRStates(snapshot); err != nil {
		t.Fatalf("SavePRStates failed: %v", err)
	}

	loaded, _ := Load()
	numbers := []string{}
	for _, p := range loaded.WatchedPRs {
		numbers = append(numbers, fmt.Sprintf("%d:%s", p.Number, p.LastKnownState))
	}
	if got := strings.Join(numbers, ","); got != "1:failure,3:" {
		t.Errorf("watch list = %s, want 1:failure,3: (#2 must stay removed, #3 must survive)", got)
	}
	if loaded.WebhookURL != "https://hooks.example.com/x" {
		t.Errorf("settings changed meanwhile must survive, got %q", loaded.WebhookURL)
	}
}

func TestSavePRStates_NoMatchDoesNotWrite(t *testing.T) {
	path := useTempConfig(t)

	if err := SavePRStates([]WatchedPR{pr(1)}); err != nil {
		t.Fatalf("SavePRStates failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("nothing should be written when no watched PR matches")
	}
}
