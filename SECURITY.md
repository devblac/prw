# Security Policy

## Overview

`prw` is a CLI tool that interacts with GitHub's API using a Personal Access Token (PAT). Security considerations primarily involve token handling and API interactions.

## Supported Versions

We provide security updates for the following versions:

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |
| < 1.0   | :x:                |

Since this is a pre-1.0 project, we focus security efforts on the `main` branch. Once v1.0 is released, we'll maintain security patches for stable releases.

## Security Best Practices

### GitHub Token Security

`prw` requires a GitHub Personal Access Token to function. Follow these guidelines:

1. **Use minimal scopes**: 
   - For public repositories: `public_repo` scope only
   - For private repositories: `repo` scope
   - Never use tokens with admin or delete permissions

2. **Protect your token**:
   - Store in environment variables, not in code
   - Use the config file only if you trust file permissions
   - Config files are created with `0600` permissions (owner read/write only)

3. **Token rotation**:
   - Regenerate tokens periodically
   - Revoke tokens immediately if compromised
   - Use fine-grained personal access tokens when available

4. **Environment considerations**:
   - Don't commit `.prw/config.json` to version control
   - Clear shell history if you pasted tokens in commands
   - Avoid running `prw` in untrusted environments

### Data Storage

- Configuration and state are stored in `~/.prw/config.json`
- This file may contain your GitHub token if set via `prw config set github_token`
- File permissions are set to `0600` on creation
- No data is sent to external services except:
  - GitHub API (required for functionality)
  - Your configured webhook URL (optional, user-controlled)

### Network Security

- All GitHub API requests use HTTPS
- Webhook notifications use the URL you provide (ensure it uses HTTPS)
- No telemetry or analytics are collected
- No background connections beyond GitHub API polling

## Reporting a Vulnerability

If you discover a security vulnerability in `prw`, please report it responsibly.

### Where to report

**Preferred**: Use GitHub Security Advisories
- Navigate to the repository's Security tab
- Click "Report a vulnerability"
- Provide details privately

**Alternative**: Email the maintainer
- Send to: `YOUR_EMAIL@example.com` (update this with actual contact)
- Use subject line: `[SECURITY] prw vulnerability report`
- Include:
  - Description of the vulnerability
  - Steps to reproduce
  - Potential impact
  - Suggested fix (if you have one)

### What to expect

- **Acknowledgment**: Within 48 hours
- **Initial assessment**: Within 1 week
- **Status updates**: Weekly until resolved
- **Fix timeline**: Depends on severity
  - Critical: 1-7 days
  - High: 1-4 weeks
  - Medium/Low: Best effort

### Disclosure Policy

- We practice responsible disclosure
- We'll work with you to understand and fix the issue
- We'll credit you in the security advisory (unless you prefer anonymity)
- Please allow us time to fix before public disclosure
- We'll coordinate timing of public disclosure with you

## Known Considerations

### Token Exposure Risks

- If `github_token` is set in config, it's stored in plaintext (encrypted storage is on the roadmap)
- Use environment variables when possible: `export GITHUB_TOKEN="..."`
- Review your shell history if you set tokens via command line

### Rate Limiting

- `prw` respects GitHub API rate limits
- Default polling interval (20s) is conservative
- Aggressive polling may exhaust rate limits
- Use a dedicated token if you run multiple instances

### Webhook Security

- Webhook URLs you configure are called without authentication by default
- Use webhook secrets or signature validation on the receiving end
- Don't expose webhook endpoints publicly without protection
- Consider using HMAC or token-based auth in your webhook handler

## Security Updates

Security fixes are announced via:

- GitHub Security Advisories
- Release notes
- Git tags

Subscribe to repository releases to stay informed.

## Questions?

For security-related questions that aren't vulnerabilities:

- Open a public issue (for general questions)
- Email the maintainer (for sensitive topics)

Thank you for helping keep `prw` secure!
