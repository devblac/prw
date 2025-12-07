# Quick Start Guide

This guide will get you up and running with `prw` in under 5 minutes.

## Prerequisites

- Go 1.22+ installed
- A GitHub Personal Access Token with `repo` scope
- Git (for cloning)

## Installation

### Build from source

```bash
# Clone the repository
git clone https://github.com/devblac/prw.git
cd prw

# Build the binary
make build

# Optionally install to $GOPATH/bin
make install
```

The binary will be available at `./bin/prw` (or `prw` in your PATH if you ran `make install`).

## Setup

### 1. Get a GitHub Token

1. Go to https://github.com/settings/tokens
2. Click "Generate new token" (classic)
3. Give it a name like "prw CLI"
4. Select scope: `repo` (or just `public_repo` for public repositories)
5. Click "Generate token"
6. Copy the token (starts with `ghp_`)

### 2. Configure the token

**Option A: Environment variable (recommended)**

```bash
# Linux/macOS
export GITHUB_TOKEN="ghp_your_token_here"

# Windows PowerShell
$env:GITHUB_TOKEN="ghp_your_token_here"
```

**Option B: Config file**

```bash
./bin/prw config set github_token "ghp_your_token_here"
```

## Basic Usage

### Watch a pull request

```bash
./bin/prw watch https://github.com/owner/repo/pull/123
```

The tool will fetch the PR and add it to your watch list.

### List watched PRs

```bash
./bin/prw list
```

### Start monitoring

```bash
./bin/prw run
```

This starts polling GitHub every 20 seconds. When a PR's CI status changes, you'll see:

```
🔔 Status Change Detected!
   PR: owner/repo#123
   Title: Add new feature
   Status: pending → success
   Link: https://github.com/owner/repo/pull/123
   Time: 2025-12-06T10:32:15Z
```

Press `Ctrl+C` to stop.

### Stop watching a PR

```bash
./bin/prw unwatch https://github.com/owner/repo/pull/123
```

## Advanced Configuration

### Change poll interval

```bash
# Poll every 30 seconds instead of 20
./bin/prw config set poll_interval_seconds 30
```

### Add a webhook

Send notifications to Slack, Discord, or any HTTP endpoint:

```bash
./bin/prw config set webhook_url "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
```

When a status changes, `prw` will POST a JSON payload like:

```json
{
  "type": "pr_status_change",
  "owner": "owner",
  "repo": "repo",
  "pr_number": 123,
  "title": "Add new feature",
  "previous_state": "pending",
  "current_state": "success",
  "sha": "abc123def456",
  "url": "https://github.com/owner/repo/pull/123",
  "timestamp": "2025-12-06T10:32:15Z"
}
```

### View all settings

```bash
./bin/prw config show
```

## Tips

- **Multiple PRs**: You can watch as many PRs as you want from different repositories
- **State persistence**: Your watch list persists between runs (stored in `~/.prw/config.json`)
- **Token security**: The config file has restrictive permissions (0600), but prefer using the environment variable
- **Rate limits**: Default polling (20s) is conservative. Be careful with aggressive intervals

## Troubleshooting

### "missing GITHUB_TOKEN" error

Make sure you've set the token via environment variable or config file (see Setup section).

### "GitHub API returned 404"

- Check that the PR URL is correct
- Verify your token has access to the repository (especially for private repos)

### "rate limit exceeded"

- Reduce your polling interval: `prw config set poll_interval_seconds 30`
- Use a dedicated token if you run multiple instances

### Tests won't run

Make sure you have Go 1.22+ and have run:

```bash
go mod download
```

## Next Steps

- Read the full [README.md](README.md) for detailed documentation
- Check [CONTRIBUTING.md](CONTRIBUTING.md) if you want to contribute
- Review [SECURITY.md](SECURITY.md) for security best practices

## Getting Help

- Open an issue on GitHub for bugs or feature requests
- Check existing issues for solutions
- Read the README for detailed usage information

Happy PR watching! 🚀

