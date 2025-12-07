# prw - Pull Request Watcher

[![CI](https://github.com/devblac/prw/workflows/CI/badge.svg)](https://github.com/devblac/prw/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/devblac/prw)](https://goreportcard.com/report/github.com/devblac/prw)
[![codecov](https://codecov.io/gh/devblac/prw/branch/main/graph/badge.svg)](https://codecov.io/gh/devblac/prw)

A lightweight CLI tool for monitoring GitHub pull request CI status changes. Stop context-switching and let `prw` notify you when your builds complete.

## What is this?

If you've ever found yourself refreshing a pull request page waiting for CI to finish, this tool is for you. `prw` watches one or more GitHub PRs and notifies you the moment their combined CI status changes—whether it's pending → success, pending → failure, or any other transition.

It runs entirely on your machine. No servers, no accounts, just a simple CLI that polls GitHub's API and alerts you when something changes. You can configure webhook notifications to push updates to Slack, Discord, or any HTTP endpoint without standing up infrastructure.

The tool is deliberately minimal. It does one thing well: watch PRs and tell you when their status changes.

## Features

- **Watch multiple PRs** from different repositories simultaneously
- **Instant notifications** when CI status changes (pending, success, failure, error)
- **Webhook support** for Slack, Discord, or custom integrations
- **Terminal notifications** with clear, actionable output
- **Configurable polling** interval to balance responsiveness and API usage
- **Persistent state** - remembers watched PRs between sessions
- **Zero dependencies** beyond your GitHub Personal Access Token

## Installation

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

### System PATH

After building, you can install to your Go bin directory:

```bash
make install
```

Or copy the binary manually to somewhere in your `$PATH`.

## Quickstart

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

Press `Ctrl+C` to stop.

### 4. Manage your watch list

List all watched PRs:

```bash
prw list
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
