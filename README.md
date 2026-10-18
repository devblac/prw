# prw - Pull Request Watcher

[![CI](https://github.com/devblac/prw/workflows/CI/badge.svg)](https://github.com/devblac/prw/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/devblac/prw)](https://goreportcard.com/report/github.com/devblac/prw)
[![codecov](https://codecov.io/gh/devblac/prw/branch/main/graph/badge.svg)](https://codecov.io/gh/devblac/prw)
[![Go Version](https://img.shields.io/badge/go-1.22-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A lightweight CLI tool for monitoring GitHub pull request CI status changes. Stop context-switching and let `prw` notify you when your builds complete.

## Metrics

- **CI Status**: All tests passing on `main` (see badge above)
- **Test Coverage**: ~92% across packages (including CLI commands)
- **Build**: Cross-platform binaries for Linux, macOS, and Windows via GitHub Releases

## What is this?

If you've ever found yourself refreshing a pull request page waiting for CI to finish, this tool is for you. `prw` watches one or more GitHub PRs and notifies you the moment their combined CI status changes—whether it's pending → success, pending → failure, or any other transition.

It runs entirely on your machine. No servers, no accounts, just a simple CLI that polls GitHub's API and alerts you when something changes. You can configure webhook notifications to push updates to Slack, Discord, or any HTTP endpoint without standing up infrastructure.

The tool is deliberately minimal. It does one thing well: watch PRs and tell you when their status changes.

## Why prw?

- **Instant signal**: Stop polling GitHub tabs; get alerted when CI flips.
- **One-command broadcast**: Share PR health to Slack/Discord without bots or servers.
- **Local-first**: Runs on your machine; tokens stay local; minimal scopes.
- **5-minute setup**: `go install` (or download release), set `GITHUB_TOKEN`, `prw watch`, `prw run`.

## Features

- **`prw wait`**: block until a PR's CI finishes and exit 0/1/2 — for scripts, pipelines and AI coding agents
- **MCP server** (`prw mcp`): lets Claude Code, Claude Desktop, Cursor and other agents check and wait for PR CI as tools
- **Auto-watch**: `prw watch --mine` / `--review-requested` follow your PRs and review requests across all repositories, no URLs needed
- **Auto-cleanup**: merged/closed PRs are notified once and dropped from the watch list by `prw run`
- **Watch multiple PRs** from different repositories simultaneously
- **Instant notifications** when CI status changes (pending, success, failure, error)
- **Review & merge events**: approved, changes requested, merge conflicts, ready to merge
- **Chat-ops broadcast**: one command to push current PR status to Slack/Discord (`prw broadcast`)
- **Webhook support** for Slack, Discord, or custom integrations
- **Native OS notifications** via system toasts (macOS/Linux/Windows)
- **Terminal notifications** with clear, actionable output
- **Configurable polling** interval to balance responsiveness and API usage
- **Persistent state** - remembers watched PRs between sessions
- **PR titles** shown alongside PR numbers in lists and notifications
- **JSON output** for automation via `prw list --json`
- **Zero dependencies** beyond your GitHub Personal Access Token

### Killer feature: Chat-ops broadcast

- Notify your team with a single command:
  ```bash
  prw broadcast --filter failing --webhook https://hooks.slack.com/services/...
  ```
- Supports filters: `all`, `changed`, `failing`
- Dry-run mode to preview output without sending

## Examples

- `examples/config.example.json` — starter config with placeholders
- `examples/env.example` — minimal env file for `GITHUB_TOKEN`

## Installation

### Prebuilt binaries (recommended)

Download the latest release for your platform from the [Releases page](https://github.com/devblac/prw/releases).

Each release includes binaries for:
- Linux (amd64, arm64)
- macOS (amd64, arm64) 
- Windows (amd64)

```bash
# Example for Linux amd64. Release asset names include the version, so resolve the
# latest tag first instead of hardcoding one that will go stale.
VERSION=$(curl -fsSL https://api.github.com/repos/devblac/prw/releases/latest | grep '"tag_name"' | cut -d'"' -f4)
curl -LO "https://github.com/devblac/prw/releases/download/${VERSION}/prw_${VERSION}_linux_amd64.tar.gz"
tar -xzf "prw_${VERSION}_linux_amd64.tar.gz"

# Make it executable and move to PATH
chmod +x "prw_${VERSION}_linux_amd64"
sudo mv "prw_${VERSION}_linux_amd64" /usr/local/bin/prw

# Verify installation
prw version
```

### From source

If you have Go 1.22+ installed:

```bash
go install github.com/devblac/prw/cmd/prw@latest
```

Or clone and build locally:

```bash
git clone https://github.com/devblac/prw.git
cd prw
make build
# Binary will be in ./bin/prw
```

To install to your Go bin directory:

```bash
make install
```

## Quickstart (5 minutes)

### 1. Set up your GitHub token

**If you use the [GitHub CLI](https://cli.github.com/) and have run `gh auth login`, there is nothing to do** — `prw` reuses that login automatically.

Otherwise `prw` needs a GitHub Personal Access Token. A classic token only requires `repo` scope (or `public_repo` for public repos only). Fine-grained tokens need read access to **Pull requests**, **Commit statuses**, and **Checks**.

`prw` combines GitHub Actions check runs with legacy commit statuses (Jenkins, CircleCI, etc.), so it works regardless of which CI you use.

Create a token at: https://github.com/settings/tokens

Then export it (`GH_TOKEN` works too):

```bash
export GITHUB_TOKEN="ghp_your_token_here"
```

Alternatively, store it in the config file:

```bash
prw config set github_token "ghp_your_token_here"
```

When several are available, `prw` uses the first one found in this order: config file, `GITHUB_TOKEN`, `GH_TOKEN`, `gh auth token`. `prw config show` tells you which one is in use.

### 2. Watch a pull request

```bash
prw watch https://github.com/owner/repo/pull/123
```

The tool will fetch the PR details and add it to your watch list.

**Or let prw find your PRs** — no URLs to copy:

```bash
prw watch --mine               # all your open PRs, in every repository
prw watch --review-requested   # open PRs where your review is requested (incl. your teams)
```

These follow a GitHub search instead of a fixed list: matching PRs are added right away, and `prw run` searches again every 5 minutes, so new PRs show up by themselves. Merged or closed PRs are dropped after their final notification, and review-request PRs are also dropped once your review is no longer requested. `prw list` shows which PRs were added automatically (`ADDED BY`). Undo with `prw unwatch --mine` / `--review-requested`, which removes only the PRs that search added — PRs you watched by hand are kept.

### 3. Start the watcher

```bash
prw run
```

`prw` will begin polling every 20 seconds (configurable) and print status changes to your terminal.

Single check and exit:

```bash
prw run --once
```

Press `Ctrl+C` to stop.

### 4. Broadcast to Slack/Discord (killer feature)

```bash
prw broadcast --filter all --webhook https://hooks.slack.com/services/...
```

Use `--dry-run` to preview without sending. Use `--filter failing` to only send failing/error statuses.

### Shell completion

```bash
prw completion bash         # or zsh|fish|powershell
```

Add the output to your shell profile for autocomplete.

### 5. Manage your watch list

List all watched PRs:

```bash
prw list
```
Titles are fetched from GitHub and shown alongside the PR number.

Machine-friendly output:

```bash
prw list --json
```

Example JSON:

```json
[
  {
    "owner": "kubernetes",
    "repo": "kubernetes",
    "number": 12345,
    "status": "success",
    "last_checked": "2025-12-06T10:30:00Z",
    "title": "Fix controller race condition"
  }
]
```

Stop watching a PR:

```bash
prw unwatch https://github.com/owner/repo/pull/123
```

## Waiting for CI in scripts and agents: `prw wait`

`prw wait` blocks until a PR's CI finishes and exits with a code you can branch on. The PR does not need to be on the watch list.

```bash
prw wait https://github.com/owner/repo/pull/123 && ./deploy.sh

# Machine-readable result on stdout (progress goes to stderr)
prw wait "$PR_URL" --timeout 15m --json
```

| Exit code | Meaning |
|-----------|---------|
| `0` | CI succeeded, or the PR was merged while waiting (e.g. auto-merge) |
| `1` | CI failed (`failure` or `error`) |
| `2` | Timed out (`--timeout`, default 30m) while CI was still pending |
| `3` | Other error: invalid URL, API error, missing token, PR closed without merging |
| `4` | The PR has **no CI**: no checks or statuses were reported within 3 minutes of its last update (see below) |
| `130` | Interrupted (Ctrl+C) |

It fails fast — as soon as one check fails it exits `1` with the failed checks and their links, without waiting for the rest. If someone pushes to the PR meanwhile, it follows the new head commit. Transient errors (network, 5xx, rate limits) are retried up to 3 times in a row. Polls every 15s by default (`--interval`).

With `--json`, stdout is a single object:

```json
{
  "owner": "owner",
  "repo": "repo",
  "number": 123,
  "title": "Add feature",
  "url": "https://github.com/owner/repo/pull/123",
  "sha": "abc123…",
  "state": "failure",
  "failed_checks": [{ "name": "Test", "url": "https://github.com/owner/repo/actions/runs/…" }],
  "total_checks": 4,
  "pending_checks": 1,
  "elapsed_seconds": 212.4
}
```

`state` is one of `success`, `failure`, `error`, `pending` (with `timed_out` or `interrupted` set), `merged`, `closed`, or `no_ci`.

**PRs without CI.** Some PRs never get a single check — the repository has no CI, or none runs for the PR's base branch (for example a stacked PR whose base isn't `main`). Right after a push, though, every PR briefly has zero checks while CI registers them. So prw only concludes `no_ci` once the PR has had no checks or statuses at all **and** hasn't been updated for 3 minutes; until then it reports `pending`. `no_ci` exits with `4`, not `0`, so `prw wait && deploy` doesn't deploy a PR that no CI looked at. `prw run` shows `no_ci` in `prw list` but doesn't send a notification for it, and notifies normally if CI shows up later (e.g. after the PR is retargeted).

## Using prw from AI agents: `prw mcp`

`prw mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io) server over stdio, so coding agents can check a PR's CI and wait for it after pushing — and read which checks failed — without screen-scraping `gh` output.

**Claude Code:**

```bash
claude mcp add prw -- prw mcp
```

**Claude Desktop, Cursor and other clients** (add to the client's `mcpServers` JSON config):

```json
{
  "mcpServers": {
    "prw": { "command": "prw", "args": ["mcp"] }
  }
}
```

Tools:

| Tool | What it does |
|------|--------------|
| `get_ci_status` | Current state of a PR (`success`, `failure`, `error`, `pending`, `merged`, `closed`, `no_ci`), check counts, failed checks with links. Returns immediately. |
| `wait_for_ci` | Blocks until CI finishes (`timeout_seconds` default 600, max 3600). Fails fast on the first failed check, follows new pushes, sends progress notifications. On timeout it returns `pending` with `timed_out: true` so the agent can call it again. |
| `list_watched` | PRs on the local watch list with their last known state. |
| `watch_pr` / `unwatch_pr` | Add or remove a PR from the watch list (a running `prw run` picks it up). |

Results include a one-line summary for the model plus the full data as JSON (and as `structuredContent`). Authentication works like every other command — with `gh auth login` there's nothing to configure. All tools share one GitHub client, so the ETag cache keeps repeated polling free.

## Configuration

Configuration is stored in `~/.prw/config.json`. You can manage settings via the `config` subcommand.

### View current configuration

```bash
prw config show
```

### Set values

```bash
# Change poll interval to 30 seconds
prw config set poll_interval_seconds 30

# Add a webhook URL
prw config set webhook_url https://hooks.slack.com/services/YOUR/WEBHOOK/URL
```

### Example config file

```json
{
  "poll_interval_seconds": 20,
  "notification_filter": "change",
  "webhook_url": "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXX",
  "notification_native": false,
  "github_token": "",
  "watched_prs": [
    {
      "owner": "kubernetes",
      "repo": "kubernetes",
      "number": 12345,
      "last_known_sha": "abc123def456",
      "last_known_state": "success",
      "last_checked": "2025-12-06T10:30:00Z",
      "title": "Fix controller race condition"
    }
  ]
}
```

### Configuration keys

- **`poll_interval_seconds`**: How often to poll GitHub (default: 20)
- **`webhook_url`**: Optional HTTP endpoint for notifications
- **`webhook_format`**: Payload format: `auto` (default, detected from the URL), `slack`, `discord`, or `generic`
- **`notification_native`**: Enable native OS notifications (true/false, default: false)
- **`pr_events`**: Also notify about reviews and merge conflicts / readiness (true/false, default: true)
- **`github_token`**: GitHub Personal Access Token (prefer env var `GITHUB_TOKEN`)

## Notifications

### Terminal

Status changes are always printed to stdout with clear formatting:

```
🔔 Status Change Detected!
   PR: kubernetes/kubernetes#12345
   Title: Fix controller race condition
   Status: pending → success
   Link: https://github.com/kubernetes/kubernetes/pull/12345
   Time: 2025-12-06T10:32:15Z
```

### Native OS Notifications

Enable native system notifications for desktop alerts without hosting a webhook:

```bash
# Enable via flag
prw run --notify-native

# Or persist in config
prw config set notification_native true
```

**Platform requirements:**
- **macOS**: Uses `osascript` (built-in)
- **Linux**: Requires `notify-send` (usually from `libnotify-bin` package)
- **Windows**: Uses Windows PowerShell (built-in) to show a toast. If nothing appears, check that Focus Assist / Do Not Disturb is off

If the required tool is missing, `prw` will log a warning and continue polling without crashing. Native notifications work alongside console and webhook notifications—you can enable all three simultaneously.

### Webhooks

If you configure a `webhook_url`, `prw` will POST to it on every status change. The payload shape is chosen from the URL:

| URL | Format | What you get |
|-----|--------|--------------|
| `https://hooks.slack.com/...` (or GovSlack `hooks.slack-gov.com`) | `slack` | A message with the PR link, status transition and failed checks |
| `https://discord.com/api/webhooks/...` | `discord` | A colored embed (green / red / yellow) with the same details |
| anything else | `generic` | Raw JSON (below), for your own services |

Override the detection with `prw config set webhook_format <auto|slack|discord|generic>` — for example `slack` for Slack-compatible services such as Mattermost.

The `generic` payload:

```json
{
  "type": "pr_status_change",
  "owner": "kubernetes",
  "repo": "kubernetes",
  "pr_number": 12345,
  "title": "Fix controller race condition",
  "previous_state": "pending",
  "current_state": "failure",
  "sha": "abc123def456",
  "failed_checks": [
    { "name": "Unit Tests", "url": "https://github.com/kubernetes/kubernetes/runs/1" }
  ],
  "url": "https://github.com/kubernetes/kubernetes/pull/12345",
  "timestamp": "2025-12-06T10:32:15Z"
}
```

`failed_checks` is omitted when nothing failed. If the endpoint rejects the request, the error includes the response body (e.g. Slack's `invalid_payload`), and requests time out after 10 seconds.

### Notification filters

Control when notifications fire:

```bash
# Only when a PR turns red
prw run --on fail

# Only when a PR turns green
prw run --on success

# Default: any state change
prw run --on change
```

The same setting can be persisted via `prw config set notification_filter <value>`.

### Reviews and merge conflicts

Besides CI, `prw run` notifies when a PR's review decision or merge state changes:

| Event | When |
|-------|------|
| `approved` | A reviewer approved and nobody's latest review requests changes |
| `changes_requested` | A reviewer's latest review requests changes |
| `conflict` | The PR has merge conflicts with its base branch |
| `ready` | GitHub says the PR can be merged, after branch protection, conflicts or an outdated branch were holding it back |

```
🔔 Review Update!
   PR: kubernetes/kubernetes#12345
   Review: none → approved
```

Webhooks get the same shapes as CI changes, with `"type": "pr_review_change"` or `"pr_merge_state_change"` in the generic payload (and `Review:` / `Merge:` instead of `Status:` in Slack and Discord messages). With `--on fail` you only get `changes_requested` and `conflict`; with `--on success`, `approved` and `ready`.

A few rules keep this quiet: nothing is announced the first time a PR is checked; `ready` isn't sent together with the CI or approval notification that caused it, and not at all for repositories without branch protection (there, "CI passed" already means it); dismissed reviews and new review rounds don't notify until someone approves or requests changes. `prw list` shows the last review and merge state in the `REVIEW` and `MERGE` columns, `prw wait --json` and the MCP tools include `merge_state`.

Turn these off with `prw config set pr_events false`. They cost one extra request per PR per poll (the reviews), answered with a free `304` when nothing changed.

### Merged and closed PRs

Once `prw run` sees that a watched PR was merged or closed, it sends one final notification (`CurrentState` is `merged` or `closed`, so you'll see e.g. `success → merged`) and removes the PR from the watch list — regardless of `--on`/`notification_filter`, since a merge is a lifecycle event, not a CI result. This keeps the watch list from growing forever. `prw list` and `prw broadcast` are unaffected: a PR only drops out once `prw run` has actually polled it.

## Troubleshooting

- **missing GITHUB_TOKEN**: run `gh auth login`, set `GITHUB_TOKEN`/`GH_TOKEN`, or use `prw config set github_token <token>`. `prw config show` shows which source (if any) was found.
- **Webhook fails**: verify URL, check HTTP 2xx, try `prw broadcast --dry-run` first.
- **Rate limits**: `prw` uses conditional requests (ETags), so a poll where nothing changed costs no API quota when authenticated — in practice only PRs whose CI is actively changing use quota. If GitHub still reports a rate limit, `prw run` pauses all checks until the limit resets (and says so), and `prw wait` waits for the reset instead of failing. Unauthenticated use is limited to 60 requests/hour: run `gh auth login` or set a token.

## Uninstall / cleanup

- Remove binary: delete `prw` from your PATH (e.g., `/usr/local/bin/prw`).
- Remove config/state: delete `~/.prw/` if you want a clean slate.

## Version Information

Check your installed version:

```bash
prw version
```

This displays the version and commit SHA (if built from a tagged release or via `make build`).

## Development

### Prerequisites

- Go 1.22 or later
- Make (optional, but recommended)

### Build

```bash
make build
```

### Run tests

```bash
make test
```

### Run integration tests

```bash
make integration
```

These hit the real GitHub API (using `GITHUB_TOKEN` if set, unauthenticated otherwise) against fixed objects in this repo, and also run nightly in CI. They are excluded from `make test`.

### Check coverage

```bash
make coverage
```

### Lint

```bash
make lint
```

Linting uses `go vet` by default. If you have `golangci-lint` installed, it will use that too.

### Clean

```bash
make clean
```

## Roadmap

These features might come in future versions:

- **GitHub App integration** for webhook-based notifications (no polling)
- **Multiple notification channels** (email, Telegram, etc.)
- **Rich filtering** (watch only specific check suites, ignore draft PRs)
- **PR review status** tracking (approvals, requested changes)
- **Interactive TUI** for managing watches

Have an idea? Open an issue!

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## Security

This tool handles your GitHub token. Store it securely and use minimal scopes. See [SECURITY.md](SECURITY.md) for details on reporting vulnerabilities.

## License

MIT License - see [LICENSE](LICENSE) for details.

---

**Questions or issues?** Open a GitHub issue or discussion. Pull requests welcome!

---

## Attribution

The original idea behind this project was proposed by Mariano Gappa.

This repository contains my own implementation and evolution of that idea.
Any mistakes or design decisions in this codebase are mine.

Original inspiration:
https://github.com/marianogappa
