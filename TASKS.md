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

## Roadmap (prioritized by importance and ROI)

Legend — Impact: 1–5 · Effort: S (≤½ day), M (1–2 days), L (3+ days) · ROI = impact relative to effort.

### Phase 0 — Make the core promise work (blocking)

| # | Task | Why | Impact | Effort | ROI |
|---|------|-----|--------|--------|-----|
| 1 | **Check Runs support** (GitHub Actions) — aggregate `/check-runs` + legacy `/status`, include failed check names in notifications | Actions-only repos report `pending` forever via `/status`; prw never notifies | 5 | S | ★★★★★ |
| 2 | **Slack/Discord webhook formats** — detect by URL, send `text` / `content` payloads; keep raw JSON as `generic` | Slack/Discord reject the current payload with 400 | 5 | S | ★★★★★ |
| 3 | **Fix Windows native toast + injection-safe escaping** (pass data via env vars, not string interpolation) | Current PowerShell script is syntactically broken; PR titles are attacker-controlled | 4 | S | ★★★★ |
| 4 | **Reload config each tick / atomic writes** — `prw watch` while `prw run` is running must not be lost | Running watcher overwrites the file with stale in-memory state | 4 | S | ★★★★ |
| 5 | **Auto-remove merged/closed PRs** (+ notify "merged") | Watch list grows forever, wastes API quota | 3 | S | ★★★★ |
| 6 | **Robustness quick wins** — webhook HTTP timeout, `MultiNotifier` continues on error, fix README version URLs | Silent hangs / lost notifications | 3 | S | ★★★★ |
| 7 | **Live integration smoke test** against a public repo (unauthenticated, CI nightly) | Mocked tests hid the Check Runs bug | 3 | S | ★★★ |

### Phase 1 — Differentiate from `gh pr checks --watch`

| # | Task | Why | Impact | Effort | ROI |
|---|------|-----|--------|--------|-----|
| 8 | **`gh` extension distribution** (`gh prw`, reuse `gh auth token`) | Zero-setup auth + discoverability in the gh extension catalog | 5 | M | ★★★★ |
| 9 | **API efficiency** — ETag/conditional requests (304s are free), rate-limit awareness + backoff | Current design exceeds 5k req/h at ~14 PRs | 4 | M | ★★★ |
| 10 | **Auto-watch "my open PRs"** (`prw watch --mine`, `--review-requested`) | Removes manual URL copy-paste — main friction | 4 | M | ★★★ |
| 11 | **Review & mergeability events** — approved, changes requested, conflicts, ready-to-merge | Covers the full PR lifecycle, not just CI | 4 | M | ★★★ |
| 12 | **`prw checks <PR>`** — table of individual check runs with links | Actionable debugging view | 3 | S | ★★★ |

### Phase 2 — AI-agent angle and team features

| # | Task | Why | Impact | Effort | ROI |
|---|------|-----|--------|--------|-----|
| 13 | **`prw wait <PR>`** — block until CI settles, exit code 0/1, print failed checks (+ `--json`) | Scriptable; ideal for coding agents and pipelines | 4 | S | ★★★★ |
| 14 | **MCP server** (`prw mcp`: `wait_for_ci`, `get_failed_checks`, `list_watched`) | Agents opening PRs need to wait on CI and read failures | 4 | M | ★★★ |
| 15 | **Failed job log excerpt** (Actions job logs tail) | Agents/devs fix without opening the browser | 3 | M | ★★ |
| 16 | Flakiness detection + status history | Team value, depends on #1 | 3 | L | ★★ |
| 17 | Homebrew/Scoop packaging, secure token storage (OS keychain) | Adoption & security polish | 2 | M | ★★ |

### Deprioritized (low ROI for now)

- TUI dashboard (`prw ui`), notification plugin system, Prometheus metrics, config format choice, GitHub App/webhook mode.

## In Progress

- ✅ #1 Check Runs support — done (verified live against `devblac/prw`: legacy `pending`/0 statuses + 2 check runs → `success`)
- ✅ #2 Slack/Discord webhook formats — done (`webhook_format` config, auto-detected from URL; also covers the webhook timeout part of #6)
- ✅ #3 Windows toast + injection-safe escaping — done (verified on Windows: old script fails to parse; new script runs and a hostile title executes nothing)
- ✅ #4 Reload config each tick / atomic writes — done (locked read-modify-write via `config.Update`; watcher persists only PR state; test suite no longer clobbers the real config)
- Next: #5 Auto-remove merged/closed PRs

## Planned

See the roadmap above.

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

Last updated: 2026-09-13
