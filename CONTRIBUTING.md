# Contributing to prw

Thanks for considering contributing to `prw`! This document outlines the project philosophy, development workflow, and guidelines for submitting changes.

## Project Philosophy

`prw` is designed to be:

- **Minimal**: We prefer small, focused solutions over feature bloat
- **Readable**: Code clarity matters more than cleverness
- **Reliable**: Core functionality should work consistently
- **Dependency-light**: We avoid heavy dependencies unless they provide clear value

When proposing changes, keep these principles in mind.

## Development Setup

### Prerequisites

- Go 1.22 or later
- Git
- Make (optional but recommended)
- A GitHub account and personal access token for testing

### Clone the repository

```bash
git clone https://github.com/devblac/prw.git
cd prw
```

### Install dependencies

```bash
go mod download
```

### Build the project

```bash
make build
```

The binary will be in `./bin/prw`.

### Run tests

```bash
make test
```

### Check test coverage

```bash
make coverage
```

We aim for around 80% coverage on core packages (`internal/*`). Don't chase 100%; focus on meaningful tests that verify behavior, not implementation details.

### Run linters

```bash
make lint
```

This runs `go vet` and `golangci-lint` (if installed). Fix any issues before submitting a PR.

### Local testing workflow

1. Build: `make build`
2. Set your GitHub token: `export GITHUB_TOKEN="your_token"`
3. Run the CLI: `./bin/prw watch <PR_URL>`
4. Start the watcher: `./bin/prw run`
5. Test your changes with real PRs

## Making Changes

### Branching

- Create a feature branch from `main`: `git checkout -b feature/your-feature-name`
- Keep branches focused on a single change or feature
- Use descriptive branch names: `fix/nil-pointer-in-watcher`, `feature/add-pr-comments`

### Commit Messages

Write clear, concise commit messages:

- Use present tense: "Add feature" not "Added feature"
- Start with a verb: "Fix bug", "Add command", "Update docs"
- Keep the first line under 72 characters
- Add details in the body if needed

Good examples:
```
Add retry logic to GitHub API client

Retries up to 3 times on rate limit errors with exponential backoff.
Fixes #42
```

```
Fix race condition in watcher loop

The config was being saved concurrently while still being read.
Added mutex to protect shared state.
```

### Code Style

Follow standard Go conventions:

- Run `go fmt` before committing (or use `make fmt`)
- Use meaningful variable names; avoid single-letter names except in tiny scopes
- Keep functions small; if a function does more than one thing, split it
- Document exported types and functions with clear doc comments
- Avoid premature optimization; clarity first

### Testing

Write tests for:

- New features
- Bug fixes (add a regression test)
- Any non-trivial logic

Tests should:

- Be clear and readable
- Test behavior, not implementation
- Use table-driven tests where appropriate
- Mock external dependencies (HTTP, filesystem, etc.)

Example test structure:

```go
func TestFeature(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
        wantErr  bool
    }{
        {"valid case", "input", "output", false},
        {"error case", "bad", "", true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := Feature(tt.input)
            if tt.wantErr && err == nil {
                t.Error("expected error, got nil")
            }
            if result != tt.expected {
                t.Errorf("got %q, want %q", result, tt.expected)
            }
        })
    }
}
```

### Documentation

Update documentation when you:

- Add a new command or flag
- Change existing behavior
- Add a new configuration option

Docs to consider:
- `README.md` - user-facing features and usage
- `CONTRIBUTING.md` - development workflow changes
- Code comments - complex logic or non-obvious decisions

## Submitting a Pull Request

### Before submitting

1. Run tests: `make test`
2. Run linters: `make lint`
3. Build successfully: `make build`
4. Test manually with the built binary
5. Update relevant documentation
6. Rebase on latest `main` if needed

### PR Guidelines

- **Keep PRs small**: Easier to review, faster to merge
- **One change per PR**: Don't bundle unrelated fixes
- **Write a clear description**: Explain what and why, not just how
- **Reference issues**: Use "Fixes #123" or "Closes #456" in the description
- **Be responsive**: Address review feedback promptly

### PR Template

When opening a PR, include:

```
## What does this PR do?

Brief description of the change.

## Why is this needed?

Context or problem being solved.

## How was it tested?

Steps to reproduce or test the change.

## Related issues

Fixes #123
```

### Review Process

- Maintainers will review PRs as time permits
- Be patient; reviews may take a few days
- Address feedback constructively
- Once approved, a maintainer will merge

## Code of Conduct

Be respectful, constructive, and professional. We're all here to build something useful.

- Assume good intent
- Provide actionable feedback
- Keep discussions focused on the code, not the person

## Questions?

If you're unsure about anything:

- Open a GitHub issue for discussion
- Ask in the PR itself
- Check existing issues for similar questions

Thanks for contributing! 🎉
