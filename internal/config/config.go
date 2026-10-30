package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devblac/prw/internal/forge"
)

const (
	NotificationFilterChange  = "change"
	NotificationFilterFail    = "fail"
	NotificationFilterSuccess = "success"
)

// Config represents the application configuration and state.
type Config struct {
	// Global settings
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	WebhookURL          string `json:"webhook_url,omitempty"`
	WebhookFormat       string `json:"webhook_format,omitempty"`
	GitHubToken         string `json:"github_token,omitempty"`
	// BitbucketToken and BitbucketEmail authenticate against Bitbucket Cloud: an
	// Atlassian API token with the account's email (Basic auth), or a workspace /
	// project / repository access token alone (Bearer). See ResolveBitbucketAuth.
	BitbucketToken string `json:"bitbucket_token,omitempty"`
	BitbucketEmail string `json:"bitbucket_email,omitempty"`
	// BitbucketWorkspaces limits `prw dashboard` to these workspaces (all of the
	// token owner's when empty); BitbucketUser is whose PRs it shows (the token
	// owner when empty), as a UUID or account id.
	BitbucketWorkspaces []string `json:"bitbucket_workspaces,omitempty"`
	BitbucketUser       string   `json:"bitbucket_user,omitempty"`

	// Dashboard settings: GroupBy (auto, issue, branch, title, none), IssuePattern
	// (a regexp for issue keys) and BranchOrder (target branches in promotion order).
	GroupBy            string   `json:"group_by,omitempty"`
	IssuePattern       string   `json:"issue_pattern,omitempty"`
	BranchOrder        []string `json:"branch_order,omitempty"`
	NotificationFilter string   `json:"notification_filter,omitempty"`
	NotificationNative bool     `json:"notification_native,omitempty"`
	// DisablePREvents turns off review (approved / changes requested) and merge
	// (conflict / ready to merge) notifications; `pr_events` in `prw config`.
	DisablePREvents bool `json:"disable_pr_events,omitempty"`

	// AutoWatch lists the searches (AutoWatchMine, AutoWatchReviewRequested) whose
	// matching PRs are added to the watch list automatically.
	AutoWatch []string `json:"auto_watch,omitempty"`

	// Watched PRs
	WatchedPRs []WatchedPR `json:"watched_prs"`
}

// Auto-watch sources.
const (
	AutoWatchMine            = "mine"
	AutoWatchReviewRequested = "review-requested"
)

// AutoWatchSources are the valid auto-watch sources, in display order.
var AutoWatchSources = []string{AutoWatchMine, AutoWatchReviewRequested}

// HasAutoWatch reports whether source is enabled.
func (c *Config) HasAutoWatch(source string) bool {
	for _, s := range c.AutoWatch {
		if s == source {
			return true
		}
	}
	return false
}

// EnableAutoWatch turns source on and reports whether it was off.
func (c *Config) EnableAutoWatch(source string) bool {
	if c.HasAutoWatch(source) {
		return false
	}
	c.AutoWatch = append(c.AutoWatch, source)
	return true
}

// DisableAutoWatch turns source off and removes the PRs it added (PRs watched by hand
// are kept). It returns whether the source was on and the PRs removed.
func (c *Config) DisableAutoWatch(source string) (wasOn bool, removed []WatchedPR) {
	kept := c.AutoWatch[:0]
	for _, s := range c.AutoWatch {
		if s == source {
			wasOn = true
			continue
		}
		kept = append(kept, s)
	}
	c.AutoWatch = kept
	if len(c.AutoWatch) == 0 {
		c.AutoWatch = nil
	}

	prs := c.WatchedPRs[:0]
	for _, pr := range c.WatchedPRs {
		if pr.Source == source {
			removed = append(removed, pr)
			continue
		}
		prs = append(prs, pr)
	}
	c.WatchedPRs = prs
	return wasOn, removed
}

// WatchedPR represents a pull request being watched.
type WatchedPR struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	// Host is where the PR lives: empty for GitHub, "bitbucket.org" for Bitbucket
	// Cloud (see forge.StorageHost).
	Host           string    `json:"host,omitempty"`
	LastKnownSHA   string    `json:"last_known_sha,omitempty"`
	LastKnownState string    `json:"last_known_state,omitempty"`
	LastChecked    time.Time `json:"last_checked,omitempty"`
	Title          string    `json:"title,omitempty"`
	// LastReviewState and LastMergeState are the review decision and merge state
	// (github.Review* / github.Merge*) seen on the last check; empty until observed.
	LastReviewState string `json:"last_review_state,omitempty"`
	LastMergeState  string `json:"last_merge_state,omitempty"`
	// Source is the auto-watch source that added this PR, or empty if it was added by
	// hand with `prw watch <PR_URL>`.
	Source string `json:"source,omitempty"`
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		PollIntervalSeconds: 20,
		NotificationFilter:  NotificationFilterChange,
		WatchedPRs:          []WatchedPR{},
	}
}

// ConfigPath is a variable that returns the path to the config file.
// It's a variable so tests can override it.
var ConfigPath = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}
	return filepath.Join(home, ".prw", "config.json"), nil
}

// Load reads the config from disk. If the file doesn't exist, returns default config.
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Apply defaults for missing fields
	if cfg.PollIntervalSeconds == 0 {
		cfg.PollIntervalSeconds = 20
	}
	if cfg.WatchedPRs == nil {
		cfg.WatchedPRs = []WatchedPR{}
	}
	cfg.NotificationFilter = normalizeNotificationFilter(cfg.NotificationFilter)

	return &cfg, nil
}

// Save writes the config to disk.
func (c *Config) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to a temp file and rename it into place so that a crash or a concurrent
	// reader never sees a half-written config.
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("failed to write config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to write config file: %w", err)
	}
	if err := os.Chmod(tmpName, 0600); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to write config file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// ErrNoChanges can be returned from an Update callback to skip saving.
var ErrNoChanges = errors.New("no config changes")

// Lock timing; variables so tests can shorten them.
var (
	lockWait  = 5 * time.Second
	lockStale = 30 * time.Second
	lockPoll  = 20 * time.Millisecond
)

// Update performs a locked read-modify-write of the config file: it loads the
// current config from disk, applies fn, and saves the result. Use it instead of
// Load+Save whenever another prw process (for example `prw run`) may be writing the
// same file, so neither side overwrites the other's changes.
func Update(fn func(*Config) error) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}

	release, err := acquireLock(path)
	if err != nil {
		return err
	}
	defer release()

	cfg, err := Load()
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		if errors.Is(err, ErrNoChanges) {
			return nil
		}
		return err
	}
	return cfg.Save()
}

// acquireLock takes an advisory lock next to the config file. A lock older than
// lockStale is assumed to belong to a crashed process and is removed.
func acquireLock(configPath string) (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	lockPath := configPath + ".lock"
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			f.Close()
			return func() { os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) && !os.IsPermission(err) {
			return nil, fmt.Errorf("failed to lock config: %w", err)
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > lockStale {
			os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for config lock %s (delete it if no other prw process is running)", lockPath)
		}
		time.Sleep(lockPoll)
	}
}

// ApplyPRState copies the polling state (SHA, state, title, last check time) of pr onto
// the matching watched PR. It reports false when that PR is not in the list, for
// example because it was unwatched in the meantime.
func (c *Config) ApplyPRState(pr WatchedPR) bool {
	for i := range c.WatchedPRs {
		w := &c.WatchedPRs[i]
		if samePR(*w, pr.Host, pr.Owner, pr.Repo, pr.Number) {
			w.LastKnownSHA = pr.LastKnownSHA
			w.LastKnownState = pr.LastKnownState
			w.LastChecked = pr.LastChecked
			w.LastReviewState = pr.LastReviewState
			w.LastMergeState = pr.LastMergeState
			if pr.Title != "" {
				w.Title = pr.Title
			}
			return true
		}
	}
	return false
}

// SavePRStates persists the polling state of prs without touching anything else in the
// file: PRs added by `prw watch` or removed by `prw unwatch` while a long-running
// command was working are preserved, as are settings changed in the meantime.
func SavePRStates(prs []WatchedPR) error {
	return Update(func(c *Config) error {
		changed := false
		for _, pr := range prs {
			if c.ApplyPRState(pr) {
				changed = true
			}
		}
		if !changed {
			return ErrNoChanges
		}
		return nil
	})
}

// RemovePRs removes prs (matched by owner/repo/number) from the config file, without
// touching PRs added or removed by other commands since this process last loaded the
// config. Used to auto-drop PRs once they are merged or closed.
func RemovePRs(prs []WatchedPR) error {
	return Update(func(c *Config) error {
		changed := false
		for _, pr := range prs {
			if c.RemovePR(pr.Host, pr.Owner, pr.Repo, pr.Number) {
				changed = true
			}
		}
		if !changed {
			return ErrNoChanges
		}
		return nil
	})
}

// samePR reports whether pr is owner/repo#number on host. Owner and repository names
// are case-insensitive on GitHub and Bitbucket, so "DevBlac/PRW" and "devblac/prw" are
// the same repository. An empty host means GitHub.
func samePR(pr WatchedPR, host, owner, repo string, number int) bool {
	return pr.Number == number && forge.NormalizeHost(pr.Host) == forge.NormalizeHost(host) &&
		strings.EqualFold(pr.Owner, owner) && strings.EqualFold(pr.Repo, repo)
}

// MarkManual clears the auto-watch Source of a watched PR, so that disabling that
// auto-watch source no longer removes it. It reports whether anything changed.
func (c *Config) MarkManual(host, owner, repo string, number int) bool {
	for i := range c.WatchedPRs {
		if samePR(c.WatchedPRs[i], host, owner, repo, number) && c.WatchedPRs[i].Source != "" {
			c.WatchedPRs[i].Source = ""
			return true
		}
	}
	return false
}

// AddPR adds a PR to the watched list if not already present.
func (c *Config) AddPR(pr WatchedPR) bool {
	for _, existing := range c.WatchedPRs {
		if samePR(existing, pr.Host, pr.Owner, pr.Repo, pr.Number) {
			return false
		}
	}
	c.WatchedPRs = append(c.WatchedPRs, pr)
	return true
}

// RemovePR removes a PR from the watched list.
func (c *Config) RemovePR(host, owner, repo string, number int) bool {
	for i, pr := range c.WatchedPRs {
		if samePR(pr, host, owner, repo, number) {
			c.WatchedPRs = append(c.WatchedPRs[:i], c.WatchedPRs[i+1:]...)
			return true
		}
	}
	return false
}

// UpdatePR updates the state of a watched GitHub PR.
func (c *Config) UpdatePR(owner, repo string, number int, sha, state string) {
	for i := range c.WatchedPRs {
		if samePR(c.WatchedPRs[i], forge.GitHub, owner, repo, number) {
			c.WatchedPRs[i].LastKnownSHA = sha
			c.WatchedPRs[i].LastKnownState = state
			c.WatchedPRs[i].LastChecked = time.Now()
			return
		}
	}
}

// Where ResolveToken found the GitHub token.
const (
	TokenSourceNone      = "not set"
	TokenSourceConfig    = "config file"
	TokenSourceEnv       = "environment variable (GITHUB_TOKEN)"
	TokenSourceGHEnv     = "environment variable (GH_TOKEN)"
	TokenSourceGitHubCLI = "GitHub CLI (gh auth token)"

	ghAuthTokenTimeout    = 5 * time.Second
	ghAuthTokenGitHubHost = "github.com"
)

// GHAuthToken asks the GitHub CLI for its token, so anyone logged in with
// `gh auth login` (including users running prw as a gh extension) needs no extra
// setup. It is a variable so tests can replace it and never run the real gh.
var GHAuthToken = func() (string, error) {
	path, err := exec.LookPath("gh")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghAuthTokenTimeout)
	defer cancel()
	// prw only talks to api.github.com, so ask for that host's token explicitly
	// rather than whatever GH_HOST points to.
	out, err := exec.CommandContext(ctx, path, "auth", "token", "--hostname", ghAuthTokenGitHubHost).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveToken returns the GitHub token and where it came from, checking in order:
// the config file, GITHUB_TOKEN, GH_TOKEN, and finally `gh auth token`.
func (c *Config) ResolveToken() (token, source string) {
	if c.GitHubToken != "" {
		return c.GitHubToken, TokenSourceConfig
	}
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t, TokenSourceEnv
	}
	if t := os.Getenv("GH_TOKEN"); t != "" {
		return t, TokenSourceGHEnv
	}
	if t, err := GHAuthToken(); err == nil && t != "" {
		return t, TokenSourceGitHubCLI
	}
	return "", TokenSourceNone
}

// GetToken returns the GitHub token (see ResolveToken), or "" if none is available.
func (c *Config) GetToken() string {
	token, _ := c.ResolveToken()
	return token
}

// BitbucketAuth is how to authenticate against Bitbucket Cloud. With an Email, Token
// is an Atlassian API token used with Basic auth; without one, Token is an access
// token sent as Bearer. A zero BitbucketAuth means anonymous (public repositories).
type BitbucketAuth struct {
	Email string
	Token string
}

// ResolveBitbucketAuth returns the Bitbucket credentials and where they came from:
// the config file (bitbucket_token / bitbucket_email) or the BITBUCKET_TOKEN /
// BITBUCKET_EMAIL environment variables.
func (c *Config) ResolveBitbucketAuth() (BitbucketAuth, string) {
	if c.BitbucketToken != "" {
		return BitbucketAuth{Email: c.BitbucketEmail, Token: c.BitbucketToken}, TokenSourceConfig
	}
	if t := os.Getenv("BITBUCKET_TOKEN"); t != "" {
		email := os.Getenv("BITBUCKET_EMAIL")
		if email == "" {
			email = c.BitbucketEmail
		}
		return BitbucketAuth{Email: email, Token: t}, "environment variable (BITBUCKET_TOKEN)"
	}
	return BitbucketAuth{}, TokenSourceNone
}

// normalizeNotificationFilter applies defaults and validation for the notification filter.
func normalizeNotificationFilter(value string) string {
	filter := strings.ToLower(strings.TrimSpace(value))
	switch filter {
	case NotificationFilterFail, NotificationFilterSuccess, NotificationFilterChange:
		return filter
	default:
		return NotificationFilterChange
	}
}

// IsValidNotificationFilter reports whether the provided filter is allowed.
func IsValidNotificationFilter(value string) bool {
	filter := strings.ToLower(strings.TrimSpace(value))
	return filter == NotificationFilterFail || filter == NotificationFilterSuccess || filter == NotificationFilterChange
}

// NormalizeNotificationFilter sanitizes user input and applies defaults.
func NormalizeNotificationFilter(value string) string {
	return normalizeNotificationFilter(value)
}
