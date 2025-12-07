# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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

