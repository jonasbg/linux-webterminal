---
difficulty: Intermediate
duration: 2-3 hours
tags: [docker, podman, dockerfile, trivy, hadolint, security]
---

# Docker Workshop

Build containers the right way - small, secure, and immutable.

## What you'll learn

- Write multi-stage Dockerfiles that produce minimal images
- Compare image sizes: Alpine vs Ubuntu vs Go builder vs Scratch
- Scan images for vulnerabilities using **Trivy**
- Lint Dockerfiles with **Hadolint** for best practices
- Understand container immutability and why it matters
- Security hardening: non-root users, capability dropping, read-only filesystems

## The workshop

You have a multi-stage `Dockerfile` in your home directory. The `instructions.md` walks you through 7 parts where you build images, compare sizes, scan for CVEs, and apply security best practices.

This environment has full container access - you can run `podman build`, `podman run`, and pull images from registries.

## Who is this for?

Developers and ops people who write Dockerfiles but want to level up from "it works" to "it's production-ready." You should be comfortable with basic Docker/Podman commands.
