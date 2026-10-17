# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `prw run` now notifies once (`merged` or `closed`) and automatically stops watching a PR once it is merged or closed, instead of polling it forever. This notification always fires, regardless of `--on`/`notification_filter`.

### Added
- `prw mcp`: a Model Context Protocol server over stdio for AI coding agents, with tools `get_ci_status`, `wait_for_ci` (progress notifications, cancellation, fail-fast), `list_watched`, `watch_pr` and `unwatch_pr`. Implemented without new dependencies (the official Go SDK needs Go 1.25).
- Conditional requests (ETags): repeated polls of unchanged resources are answered with `304 Not Modified`, which doesn't count against the authenticated rate limit. Measured against the real API: after the first poll, 5 further polls of a PR (15 requests) consumed 0 quota. Previously each watched PR cost 3 requests per poll (~540/hour at the default 20s interval), so about 9 PRs exhausted the 5,000/hour limit.
- Rate-limit handling: 403/429 rate-limit responses are told apart from missing-permission 403s. `prw run` stops the current round and pauses until the reset time; `prw wait` waits for the reset (still bounded by `--timeout`) instead of giving up.
- `prw wait <PR_URL>`: blocks until the PR's CI finishes and exits `0` (success or merged), `1` (failed), `2` (timeout), `3` (other error) or `130` (interrupted). Fails fast on the first failed check, follows new pushes, retries transient API errors, and prints a JSON result with `--json`. Progress goes to stderr.
- Zero-setup auth for GitHub CLI users: when no token is configured, `prw` uses `GH_TOKEN` or, failing that, the token from `gh auth token`. Lookup order: config file, `GITHUB_TOKEN`, `GH_TOKEN`, `gh auth token`. `prw config show` now reports the exact source.
- Integration tests against the real GitHub API (`make integration`, build tag `integration`), run nightly by a new `Integration` workflow. They fail against the old status-only logic.

### Fixed
- Requests without a token no longer send an empty `Authorization: Bearer ` header, which GitHub rejects with 401 even for public repositories.
- `MultiNotifier` (used whenever more than one notification channel is enabled) stopped at the first failing notifier. A down webhook silently suppressed the console and native notifications that were supposed to run after it; now every notifier always runs and their errors are combined.
- The README's "Prebuilt binaries" install snippet was pinned to `v0.2.0`, two releases behind the current `v0.3.0`. It now resolves the actual latest release tag first.
- `prw watch` / `prw unwatch` while `prw run` is running: the watcher used to overwrite the config file with its stale in-memory copy on every poll, silently dropping PRs you had just added and resurrecting ones you had removed. It now re-reads the watch list on every poll (changes take effect on the next tick) and saves only the polling state of the PRs, using a locked read-modify-write. `watch`, `unwatch`, `config set/unset` and `broadcast` use the same mechanism.
- Run-only overrides such as `prw run --on fail` are no longer written into the config file.
- The config file is written atomically (temp file + rename), so a crash can no longer leave it truncated.
- The test suite no longer overwrites the real `~/.prw/config.json`.
- Windows native notifications: the PowerShell script was a syntax error, so no toast was ever shown. It is rewritten to build the toast correctly.
- Security: notification text (PR titles are controlled by anyone who can open a PR) is no longer spliced into the Windows PowerShell script; it is passed through environment variables, so it cannot be executed as code. `notify-send` also receives its arguments after `--`.
- Tests no longer pop real desktop notifications.
- Slack and Discord webhooks now work: `prw` sends a Slack message (`text`) or Discord embed instead of its raw JSON, which both services rejected with HTTP 400. The format is detected from the URL and can be overridden with `prw config set webhook_format <auto|slack|discord|generic>`; other URLs keep the existing JSON payload.
- Webhook requests now time out after 10 seconds instead of waiting indefinitely, and webhook errors include the response body.
- CI status now aggregates GitHub Check Runs (GitHub Actions) together with legacy commit statuses. Previously, repositories using only GitHub Actions were reported as `pending` forever and never triggered notifications.

### Added
- Failed check names and links are included in console notifications and in the webhook payload (`failed_checks`)
- Cross-platform prebuilt binaries (Linux, macOS, Windows) via GitHub Releases
- Automated release workflow for tagged versions
- Issue and pull request templates
- This changelog

## v0.2.0 - 2025-12-07

- Added `--json` flag for `prw list`
- Added PR title caching and display
- Added notification filters (--on fail|success|change)

## [0.1.0] - 2025-12-07

### Added
- Initial public release
- Core CLI commands: `watch`, `unwatch`, `list`, `run`, `config`, `version`
- GitHub API integration for fetching PR status and combined CI state
- Local configuration and state persistence in `~/.prw/config.json`
- Webhook notifications for Slack, Discord, or custom endpoints
- Terminal notifications with clear status change alerts
- Configurable polling interval (default 20 seconds)
- Comprehensive unit tests with ~86% coverage
- GitHub Actions CI workflow
- Complete documentation: README, CONTRIBUTING, SECURITY, QUICKSTART
- MIT license

[Unreleased]: https://github.com/devblac/prw/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/devblac/prw/releases/tag/v0.2.0
[0.1.0]: https://github.com/devblac/prw/releases/tag/v0.1.0

