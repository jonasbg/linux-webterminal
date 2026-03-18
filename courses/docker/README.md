# Docker Workshop

Build small, secure and immutable containers. Multi-stage builds, Trivy scanning, and Hadolint linting.

## What's included

- **instructions.md** - Full workshop guide (7 parts, ~2.5 hours)
- **exercise/Dockerfile** - Multi-stage build example (Alpine, Ubuntu, Go, Scratch)
- **install_trivy.sh** - Script to install Trivy vulnerability scanner

## Workshop topics

1. Container fundamentals
2. Multi-stage Dockerfile exploration
3. Container size comparison
4. Vulnerability scanning with Trivy
5. Dockerfile linting with Hadolint
6. Container immutability
7. Security hardening best practices

## Security profile: relaxed

This course requires a relaxed security profile because students need to:
- Run `podman build` and `podman run` inside the container
- Pull images from registries (network access required)
- Use privileged mode for podman-in-podman

## Building

```bash
docker build -t terminal-docker .
```
