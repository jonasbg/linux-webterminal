# Linux III

Process investigation - explore the /proc filesystem, PIDs, file descriptors, and namespaces.

## What's included

- **instruction.md** - Guided walkthrough of /proc, process inspection, environment variables, file descriptors, and PID namespaces

## Security profile: strict

- No network access
- Read-only filesystem (64MB tmpfs for /home and /tmp)
- All capabilities dropped
- 64MB memory limit, 10% CPU, max 10 processes
- Whitelisted commands only

## Building

```bash
podman build -t ghcr.io/jonasbg/linux-webterminal/terminal-linux-3:latest .
```
