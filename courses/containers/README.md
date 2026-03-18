# Container Runtime Fundamentals

How containers actually work - namespaces, cgroups, and the runtime stack (runc, containerd, CRI-O, Podman).

## What's included

- **instruction.md** - Guided walkthrough with hands-on exercises that demonstrate container isolation by hitting real limits

## What students learn

- Namespaces: PID, UTS, mount, network - what each isolates
- Cgroups: PID limits, memory limits, CPU throttling - experienced firsthand
- The runtime stack: CLI → container runtime → OCI runtime (runc/crun) → process
- What a container image actually is (tarballs + JSON)
- How runc creates a container (5 syscalls)
- containerd vs CRI-O vs Podman

## Security profile: strict

Uses the strict profile intentionally - the limits ARE the lesson. Students experience cgroups by hitting them.

## Building

```bash
podman build -t ghcr.io/jonasbg/linux-webterminal/terminal-containers:latest .
```
