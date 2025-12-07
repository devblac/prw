# Development Tasks

This file tracks the project's development status and planned work. It's a living document—tasks move between sections as work progresses.

## Done

- ✅ Project structure and Go module initialization
- ✅ Core internal packages implemented:
  - `internal/config` - configuration management and persistence
  - `internal/github` - GitHub API client with PR and status fetching
  - `internal/watcher` - polling loop and state change detection
  - `internal/notify` - console and webhook notifications
  - `internal/version` - version string handling
- ✅ CLI commands with Cobra:
  - `prw watch` - add PRs to watch list
  - `prw list` - show watched PRs
  - `prw unwatch` - remove PRs from watch list
  - `prw run` - start the watcher loop
  - `prw config` - manage configuration (show/set/unset)
  - `prw version` - display version info
- ✅ Comprehensive unit tests for all internal packages
- ✅ Makefile with build, test, coverage, and lint targets
- ✅ GitHub Actions CI workflow
- ✅ Documentation:
  - README with installation, quickstart, and usage
  - CONTRIBUTING guide
  - SECURITY policy
  - This tasks file
- ✅ Phase 1 improvements:
  - `prw list --json` machine-friendly output
  - PR title fetching and display in lists/notifications
  - Notification filters via `--on` flag and config
- ✅ Release workflow and cross-platform binaries
- ✅ CHANGELOG maintenance
- ✅ Issue and PR templates

## In Progress

No active items.

## Planned

### Near-term improvements

- GitHub App integration for webhook-based notifications (eliminates polling)
- Desktop notifications using OS-native APIs (macOS, Linux, Windows)
- Retry logic with exponential backoff for transient API errors
- Better error messages when GitHub token lacks required permissions
- Local HTTP server mode (`prw serve`)
- TUI dashboard (`prw ui`)
- Notification plugin system
- Flakiness detection and handling
- Mergeability checks/rules

### Feature additions

- Watch specific check suites or workflow runs instead of combined status
- Filter PRs by state (ignore drafts, only watch open PRs, etc.)
- Track PR review status (approvals, requested changes)
- Support for GitHub Enterprise Server installations
- Multiple notification channels (email via SMTP, Telegram, etc.)

### Developer experience

- Interactive TUI mode for managing watches without separate commands
- Shell completions (bash, zsh, fish)
- Homebrew formula for easier installation

### Quality and observability

- Structured logging with levels (debug, info, warn, error)
- Metrics export (Prometheus format?) for watch duration and API calls
- Integration tests that use GitHub's API (requires test fixtures or VCR)
- Performance profiling for high PR counts

### Nice-to-haves

- Config file format choice (JSON, YAML, TOML)
- Encrypted token storage in config file
- Watch entire repositories (all PRs matching a filter)
- Historical status tracking (see when a PR became green)
- Export watch list to shareable format

## Won't Do (For Now)

These are explicitly out of scope for the initial version:

- Backend server component - keeping it CLI-only
- Database dependency - JSON file is sufficient
- Complex filtering DSL - simple is better
- Multi-user support - single-user CLI tool
- Web UI - terminal-first approach

---

## Notes

- Keep features focused on the core use case: knowing when PRs change status
- Resist feature creep; every addition adds maintenance burden
- Prioritize reliability and usability over feature count
- Community feedback will shape the roadmap

Last updated: 2025-12-07
