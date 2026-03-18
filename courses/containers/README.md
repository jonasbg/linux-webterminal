---
difficulty: Intermediate
duration: 60-90 min
tags: [containers, namespaces, cgroups, runc, containerd, podman]
---

# Container Fundamentals

Understand what a container really is - no magic, just Linux.

## What you'll learn

- A container is just a process with **namespaces** (isolation) and **cgroups** (resource limits)
- Experience cgroups firsthand: hit the PID limit, run out of memory, feel CPU throttling
- Explore your own namespaces: PID, network, mount, UTS
- The runtime stack: CLI -> containerd/CRI-O/Podman -> runc/crun -> your process
- What container images actually are (tarballs + JSON, stacked with overlayfs)
- How runc creates a container in 5 system calls
- containerd vs CRI-O vs Podman - what each does and why

## The approach

You're already inside a container with real resource limits applied. The exercises make you **hit those limits** so you understand what cgroups do by feeling them, not just reading about them.

## Who is this for?

Anyone who uses containers but wants to understand what's happening underneath. Essential knowledge before moving to orchestration (Kubernetes) or troubleshooting container issues in production.
