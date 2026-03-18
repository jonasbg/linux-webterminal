# Linux II

Git signing - learn GPG/SSH commit signing and verification.

## What's included

- **git-signing/** - A repository for practicing GPG and SSH commit signing

## Security profile: strict

- No network access
- Read-only filesystem (64MB tmpfs for /home and /tmp)
- All capabilities dropped
- 64MB memory limit, 10% CPU, max 10 processes
- Whitelisted commands only

## Building

```bash
podman build -t ghcr.io/jonasbg/linux-webterminal/terminal-linux-2:latest .
```
