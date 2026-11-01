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

- **`prw dashboard`**: a live table of all your PRs across repositories (GitHub and Bitbucket), grouped by task, one column per target branch — see how far each change and its cherry-picks got
- **`prw checks`**: every check of a PR with its result, duration and link — and with `--logs`, the output of the step that failed
- **`prw flaky`**: find a repository's flaky jobs (failed, then passed on re-run of the same commit)
- **`prw wait`**: block until a PR's CI finishes and exit 0/1/2 — for scripts, pipelines and AI coding agents
- **MCP server** (`prw mcp`): lets Claude Code, Claude Desktop, Cursor and other agents check and wait for PR CI as tools
- **Bitbucket Cloud**: watch, wait for and list the builds of Bitbucket PRs (Pipelines, Jenkins, ...) with the same commands
- **Auto-watch**: `prw watch --mine` / `--review-requested` follow your PRs and review requests across all repositories, on GitHub and Bitbucket, no URLs needed
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

These follow a search instead of a fixed list, on GitHub and — when a Bitbucket token or `bitbucket_user` is set — on Bitbucket Cloud too (see [Bitbucket Cloud](#bitbucket-cloud)): matching PRs are added right away, and `prw run` searches again every 5 minutes, so new PRs show up by themselves. Merged or closed PRs are dropped after their final notification, and review-request PRs are also dropped once your review is no longer requested. `prw list` shows which PRs were added automatically (`ADDED BY`). Undo with `prw unwatch --mine` / `--review-requested`, which removes only the PRs that search added — PRs you watched by hand are kept.

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

## All your PRs at a glance: `prw dashboard`

`prw dashboard` (alias `prw top`) is a live table of your pull requests across every repository on GitHub and Bitbucket Cloud, refreshed every minute — no need to watch them first, prw finds them.

```
$ prw dashboard --review --merged
prw dashboard · 9 open PR(s) · 14:02:11 · every 1m · Ctrl+C to quit

Your PRs (incl. merged in the last 1d) (11)
TASK       REPO                dev            rc              main|master      UPDATED  TITLE
PROJ-123   acme/billing-api    #12 🟣 merged  #15 ⏳ 3/5 ✓1    —                12m      Add spending limits
           acme/web            #7 🟣 merged   #8 ✅ CI ok ✓2   #9 ❌ CI failed  20m      Add spending limits
PROJ-98    acme/accounts       —              —               #31 ⛔ conflict  12d      Fix login timeout

To review (2)
TASK       REPO                dev            rc              main|master      UPDATED  TITLE
PAY-77     acme/payments       #44 🚀 ready   —               —                3h       Retry webhooks

API requests in the last hour: GitHub 48/h · Bitbucket 214/h
```

- **Rows are tasks**: PRs whose source branch or title share an issue key (`PROJ-123` — Jira, Linear, YouTrack…) share a row, and so do branches that only differ by cherry-pick / backport decorations (`feature/login`, `cherry-pick/feature/login-rc`). A change and its cherry-picks show up together, across repositories.
- **Columns are target branches in promotion order**: `dev|develop` → `staging|qa|uat` → `rc|release|release/*` → `main|master|prod`, merged by tier so different naming across repositories still lines up. Other targets (e.g. stacked PRs) go in one `other` column that names them.
- **Cells** show the PR number — a clickable link in most terminals — and its most pressing state: ❌ CI failed, ⛔ conflict, 📝 changes requested, ⏳ CI running (done/total), ✅ CI ok, 🚀 ready to merge, 🟣 merged; `✓N` counts approvals.
- **Filters**: your PRs by default; `--review` adds PRs where your review is requested, `--merged` your recently merged ones (`--since 24h` by default, e.g. `--since 3d`). `--mine=false` hides yours.
- `--once` prints once (also when piped), `--json` prints the PRs with their task for scripts.

**Requests stay low**: a PR's CI is only read again when its head changes, while it's running, or every 15 minutes; Bitbucket's merge checks are reused for 30 minutes while a PR is unchanged; PRs to review on Bitbucket (which can only be searched per repository) are rescanned every 10 minutes across repositories active in the last 30 days. Dozens of Bitbucket PRs fit in its 1,000 requests/hour; the footer shows the count.

**Tuning** (all optional):

```bash
prw config set group_by branch                 # auto (default), issue, branch, title, none
prw config set issue_pattern '[0-9]+-'         # what an issue key looks like in your branches
prw config set branch_order dev,qa,uat,main    # your own promotion order
prw config set bitbucket_workspaces acme,acme-labs  # default: all your workspaces
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

It fails fast — as soon as one check fails it exits `1` with the failed checks and their links, without waiting for the rest. If someone pushes to the PR meanwhile, it follows the new head commit. Transient errors (network, 5xx, rate limits) are retried up to 3 times in a row. Polls every 15s by default (`--interval`). Add `--logs` to also get the output of the failed step of each failed job (see below; `failed_logs` in `--json`).

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

## Seeing every check: `prw checks`

`prw checks` lists each check of a PR's head commit (GitHub Actions jobs, other check runs and legacy commit statuses) with its result, how long it took and a link to it — failures first, then running ones. The PR does not need to be on the watch list.

```
$ prw checks https://github.com/owner/repo/pull/123
owner/repo#123 — Add feature
abc1234 · failure · 1 fail, 1 pending, 3 pass · merge: blocked

❌ fail     Test (ubuntu-latest)  4m02s   https://github.com/owner/repo/actions/runs/…/job/…
⏳ pending  E2E                   1m10s…  https://github.com/owner/repo/actions/runs/…/job/…
✅ pass     Build                 45s     https://github.com/owner/repo/actions/runs/…/job/…
✅ pass     Lint                  21s     https://github.com/owner/repo/actions/runs/…/job/…
✅ pass     ci/jenkins                    https://jenkins.example.com/job/…
```

Runs superseded by a newer run of the same check (e.g. cancelled by `concurrency: cancel-in-progress`) are left out, as they don't count. `--json` prints the same as an object with a `checks` array (`name`, `bucket`, `state`, `kind`, `url`, `started_at`, `completed_at`). Exit codes match `prw wait` — `0` passed (or merged), `1` failed, `3` error or closed without merging, `4` no CI — plus `8` while checks are still running.

### Why did it fail? `--logs`

`prw checks --logs` (and `prw wait --logs`) also print, for each failed GitHub Actions job, the output of the step that failed: its last lines up to the error, with timestamps, colors and runner markup stripped — what you'd scroll to in the browser.

```
$ prw checks https://github.com/owner/repo/pull/123 --logs
…
── Lint · step: staticcheck ./... ──
   -: internal error in importing "cmp" (cannot decode "cmp", export data version 5 is greater than maximum supported version 4)
   -: internal error in importing "math/bits" (…)
   Error: Process completed with exit code 1.
   Full log: https://github.com/owner/repo/actions/runs/…/job/…
```

`--log-lines` sets how many lines (default 40). At most 5 job logs are downloaded per command. Checks from other CI systems (and legacy commit statuses) have no log in the API, so you get their link instead. Logs expire after GitHub's retention period (90 days by default). Fine-grained tokens need the **Actions: read** permission.

## Finding flaky checks: `prw flaky`

A job that fails and then passes when its workflow run is re-run — same commit, same code — is flaky. `prw flaky` scans a repository's recent GitHub Actions runs (300 by default, `--runs` up to 1000) for exactly that:

```
$ prw flaky grafana/grafana
grafana/grafana: scanned 300 workflow run(s) since 2026-10-24, 3 of them re-run.

FLAKED  WORKFLOW            JOB                         LAST SEEN         LATEST FAILED ATTEMPT
1x      Backend Unit Tests  Grafana Enterprise (3/8)    2026-10-24 15:32  https://github.com/grafana/grafana/actions/runs/…/job/…
1x      Integration Tests   Sqlite Enterprise (2/16)    2026-10-24 15:25  https://github.com/grafana/grafana/actions/runs/…/job/…
1x      End-to-end tests    Playwright E2E tests (5/8)  2026-10-24 07:55  https://github.com/grafana/grafana/actions/runs/…/job/…
```

It takes a repository (`owner/repo`) or any GitHub URL in it, such as a PR's. Only re-run runs can tell flakiness apart from real failures, and jobs cancelled because another job failed first don't count. `--json` for scripts. It costs a few requests per 100 runs plus a couple per re-run run; on large repositories GitHub takes a few seconds per request, so prw fetches in parallel.

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
| `get_failed_logs` | For each failed GitHub Actions job, the output of the step that failed (last `max_lines` lines up to the error, default 40). What an agent needs to fix the failure without a browser. |
| `get_flaky_checks` | A repository's jobs that failed and then passed on re-run of the same commit, with counts and links — so an agent can tell a known-flaky failure from one its change caused. |
| `wait_for_ci` | Blocks until CI finishes (`timeout_seconds` default 600, max 3600). Fails fast on the first failed check, follows new pushes, sends progress notifications. On timeout it returns `pending` with `timed_out: true` so the agent can call it again. |
| `list_watched` | PRs on the local watch list with their last known state. |
| `watch_pr` / `unwatch_pr` | Add or remove a PR from the watch list (a running `prw run` picks it up). |

Results include a one-line summary for the model plus the full data as JSON (and as `structuredContent`). Authentication works like every other command — with `gh auth login` there's nothing to configure. All tools share one GitHub client, so the ETag cache keeps repeated polling free.

## Bitbucket Cloud

prw also watches **Bitbucket Cloud** pull requests: pass their URL wherever a PR URL goes.

```bash
prw watch https://bitbucket.org/acme/api/pull-requests/42
prw wait  https://bitbucket.org/acme/api/pull-requests/42 && ./deploy.sh
prw checks https://bitbucket.org/acme/api/pull-requests/42
```

- **CI** comes from the commit's build statuses, so it covers Bitbucket Pipelines, Jenkins and anything else that reports to Bitbucket, each with its link and duration. A stopped build counts as failed.
- **Reviews**: participants who approved or requested changes. **Merge state** comes from the PR's merge checks: `conflict`, `blocked` (e.g. missing approvals or successful builds), or `ready`.
- `prw run` notifies about Bitbucket PRs just like GitHub ones, `prw list` shows them as `bitbucket:workspace/repo`, and the MCP tools accept their URLs.
- **Auto-watch**: `prw watch --mine` follows your open PRs in all your workspaces (or those in `bitbucket_workspaces`). `--review-requested` follows open PRs that list you as a reviewer and that you haven't approved or requested changes on yet; Bitbucket can only search those per repository, so prw scans the repositories active in the last 30 days (up to 60 per workspace) every 15 minutes.
- Bitbucket Cloud allows 1,000 API requests per hour, so `prw run` spaces out the checks of Bitbucket PRs whatever the poll interval: each is checked at most once a minute, and less often as the list grows (every 18 seconds × the number of Bitbucket PRs, e.g. every 9 minutes with 30), so checks use about 600 requests an hour and leave room for auto-watch and other commands. A rate limit on one service doesn't pause the other.
- Not yet for Bitbucket: `--logs` and `prw flaky`.

**Authentication.** Public repositories work without credentials. For private ones, create an [Atlassian API token](https://id.atlassian.com/manage-profile/security/api-tokens) with Bitbucket read scopes (repositories and pull requests) and give prw your Atlassian email with it:

```bash
prw config set bitbucket_email you@example.com
prw config set bitbucket_token <api-token>
# or: export BITBUCKET_EMAIL=you@example.com BITBUCKET_TOKEN=<api-token>
```

A workspace, project or repository access token also works: set only `bitbucket_token` (it's sent as a Bearer token). No GitHub token is needed if you only watch Bitbucket PRs.

## GitHub Enterprise

prw also works with **GitHub Enterprise Server** and **GHE.com**, alongside github.com: tell it where your instance is, and pass its PR URLs wherever a PR URL goes.

```bash
prw config set github_enterprise_url https://github.acme.com
gh auth login --hostname github.acme.com    # or set GH_ENTERPRISE_TOKEN
prw watch https://github.acme.com/team/api/pull/9
```

- The API is `https://<host>/api/v3` for Enterprise Server and `https://api.<subdomain>.ghe.com` for GHE.com.
- The token comes from `github_enterprise_token`, `GH_ENTERPRISE_TOKEN` / `GITHUB_ENTERPRISE_TOKEN` (the GitHub CLI's variables), or `gh auth token --hostname <host>`. Your github.com token is never sent there, nor the other way round.
- `prw run`, `wait`, `checks` (with `--logs`), `list`, `broadcast`, `dashboard`, the auto-watch searches (`--mine` / `--review-requested` cover both hosts) and the MCP tools all work with it. `prw list` shows its PRs as `github.acme.com:team/api`.
- One Enterprise instance at a time; `prw flaky` is github.com only for now.

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
- **`github_enterprise_url`** / **`github_enterprise_token`**: your GitHub Enterprise instance and its token (env `GH_ENTERPRISE_TOKEN`; see [GitHub Enterprise](#github-enterprise))
- **`bitbucket_token`** / **`bitbucket_email`**: Bitbucket Cloud API token and its Atlassian email, or an access token alone (env `BITBUCKET_TOKEN` / `BITBUCKET_EMAIL`)

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
