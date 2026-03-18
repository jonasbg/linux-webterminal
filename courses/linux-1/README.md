# Linux I

Introduction to the Linux command line.

## What's included

The clmystery files are placed directly in the home directory. Students use `grep`, `cat`, `head`, and other basic commands to solve a command-line murder mystery.

## Security profile: strict

- No network access
- Read-only filesystem (64MB tmpfs for /home and /tmp)
- All capabilities dropped
- 64MB memory limit, 10% CPU, max 10 processes
- Whitelisted commands only

## Building

```bash
podman build -t ghcr.io/jonasbg/linux-webterminal/terminal-linux-1:latest .
```
