# Web Terminal

A secure web-based terminal platform that runs isolated containers per course/task. Users pick a task from a landing page and get their own sandboxed shell environment.

## Courses

Each course has its own container image with pre-loaded materials and a security profile.

| Course | Profile | Description |
|--------|---------|-------------|
| **Linux I** | strict | clmystery command-line challenge |
| **Linux II** | strict | Process investigation, /proc, git signing |
| **Docker Workshop** | relaxed | Podman-in-podman, multi-stage builds, Trivy, Hadolint |

### Building course images

```bash
cd courses
docker compose build
```

This builds all three images:
- `ghcr.io/jonasbg/linux-webterminal/terminal-linux-1:latest`
- `ghcr.io/jonasbg/linux-webterminal/terminal-linux-2:latest`
- `ghcr.io/jonasbg/linux-webterminal/terminal-docker:latest`

### Building the server

```bash
docker build -t terminal-server .
```

### Running

```bash
# Start the server (uses docker-compose.yml in project root)
docker compose up -d

# Or run directly
docker run -p 5000:5000 --rm -it \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --name terminal-server terminal-server
```

Then open `http://localhost:5000` to see the landing page.

### Running for development

```bash
pip install -r requirements-lock.txt
python app.py
```

## Security Profiles

**Strict** (Linux I, Linux II):
- No network access
- Read-only filesystem (64MB tmpfs for /home and /tmp)
- All capabilities dropped, no-new-privileges
- 64MB memory limit, 10% CPU, max 10 processes
- /proc entries masked
- Whitelisted commands only

**Relaxed** (Docker Workshop):
- Bridge networking (needed to pull images)
- Privileged mode (needed for podman-in-podman)
- Writable filesystem with tmpfs for /run, /var/lib/containers, /tmp
- No resource limits

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HOST` | `*` | CORS allowed origins |
| `TTY_LOGGING_ENABLED` | `false` | Enable terminal session logging |
| `TTY_LOG_DIR` | `./logs` | Log directory |
| `MAX_CONTAINERS` | `10` | Max concurrent containers |
| `CONTAINER_LIFETIME` | `3600` | Auto-cleanup timeout (seconds) |
| `CONTAINER_IMAGE` | `ghcr.io/jonasbg/linux-webterminal/terminal-base:latest` | Fallback image when no course is specified |
| `PORT` | `8080` | Server listen port |

## Useful Commands

Remove all terminal containers:

```bash
docker rm -f $(docker ps -a --filter "label=app=web-terminal" -q)
```

List unique IP addresses from logs:

```bash
grep -hoP 'Origin IP: \K[\d\.]+' logs/*.log | sort -u
```
