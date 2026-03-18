# Linux I

Introduction to the Linux command line.

## What's included

- **clmystery** - A command-line murder mystery game. Navigate the filesystem, use `grep`, `cat`, `head`, and other basic commands to solve the crime.

## Security profile: strict

- No network access
- Read-only filesystem (64MB tmpfs for /home and /tmp)
- All capabilities dropped
- 64MB memory limit, 10% CPU, max 10 processes
- Whitelisted commands only

## Building

```bash
docker build -t terminal-linux-1 .
```
