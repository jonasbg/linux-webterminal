# Linux II

Advanced Linux - process investigation, /proc filesystem, and git signing.

## What's included

- **01. Linux I/** - clmystery (reference material from Linux I)
- **02. Linux II/** - Git signing repository for learning GPG/SSH commit signing
- **03. Linux II/** - Process investigation task: explore `/proc`, PIDs, file descriptors, namespaces

## Security profile: strict

- No network access
- Read-only filesystem (64MB tmpfs for /home and /tmp)
- All capabilities dropped
- 64MB memory limit, 10% CPU, max 10 processes
- Whitelisted commands only

## Building

```bash
docker build -t terminal-linux-2 .
```
