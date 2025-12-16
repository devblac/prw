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

- **Watch multiple PRs** from different repositories simultaneously
- **Instant notifications** when CI status changes (pending, success, failure, error)
- **Chat-ops broadcast**: one command to push current PR status to Slack/Discord (`prw broadcast`)
- **Webhook support** for Slack, Discord, or custom integrations
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
# Download and extract (example for Linux amd64)
curl -LO https://github.com/devblac/prw/releases/download/v0.2.0/prw_v0.2.0_linux_amd64.tar.gz
tar -xzf prw_v0.2.0_linux_amd64.tar.gz

# Make it executable and move to PATH
chmod +x prw_v0.2.0_linux_amd64
sudo mv prw_v0.2.0_linux_amd64 /usr/local/bin/prw

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

`prw` needs a GitHub Personal Access Token to query PR status. The token only requires `repo` scope (or `public_repo` for public repos only).

Create a token at: https://github.com/settings/tokens

Then export it:

```bash
export GITHUB_TOKEN="ghp_your_token_here"
```

Alternatively, store it in the config file:

```bash
prw config set github_token "ghp_your_token_here"
```

### 2. Watch a pull request

```bash
prw watch https://github.com/owner/repo/pull/123
```

The tool will fetch the PR details and add it to your watch list.

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

### Webhooks

If you configure a `webhook_url`, `prw` will POST a JSON payload on every status change:

```json
{
  "type": "pr_status_change",
  "owner": "kubernetes",
  "repo": "kubernetes",
  "pr_number": 12345,
  "title": "Fix controller race condition",
  "previous_state": "pending",
  "current_state": "success",
  "sha": "abc123def456",
  "url": "https://github.com/kubernetes/kubernetes/pull/12345",
  "timestamp": "2025-12-06T10:32:15Z"
}
```

This works with:
- **Slack**: Use incoming webhooks
- **Discord**: Use webhook URLs
- **Custom services**: Any endpoint that accepts JSON POST

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

## Troubleshooting

- **missing GITHUB_TOKEN**: set via env var or `prw config set github_token <token>`.
- **Webhook fails**: verify URL, check HTTP 2xx, try `prw broadcast --dry-run` first.
- **Rate limits**: increase `poll_interval_seconds`.

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
- **Desktop notifications** via OS-native APIs
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
