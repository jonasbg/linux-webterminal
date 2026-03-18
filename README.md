# Web Terminal

A secure web-based terminal platform that runs isolated containers per course/task. Users pick a task from a landing page and get their own sandboxed shell environment.

## Courses

Each course has its own container image with pre-loaded materials directly in the home directory.

| Course | Profile | Description |
|--------|---------|-------------|
| **Linux I** | strict | clmystery command-line challenge |
| **Linux II** | strict | Git signing - GPG/SSH commit signing |
| **Linux III** | strict | Process investigation - /proc, PIDs, file descriptors |
| **Docker Workshop** | relaxed | Podman-in-podman, multi-stage builds, Trivy, Hadolint |

## Running locally

### Prerequisites

- Podman (or Docker) with a running socket
- Python 3.12+

### 1. Build course images

```bash
cd courses
podman build -t ghcr.io/jonasbg/linux-webterminal/terminal-linux-1:latest -f linux-1/Dockerfile linux-1/
podman build -t ghcr.io/jonasbg/linux-webterminal/terminal-linux-2:latest -f linux-2/Dockerfile linux-2/
podman build -t ghcr.io/jonasbg/linux-webterminal/terminal-linux-3:latest -f linux-3/Dockerfile linux-3/
podman build -t ghcr.io/jonasbg/linux-webterminal/terminal-docker:latest -f docker/Dockerfile docker/
```

Or if you have Docker installed:

```bash
cd courses
docker compose build
```

### 2. Run the server

**Option A: Run directly (development)**

```bash
pip install -r requirements-lock.txt
python app.py
```

Server starts on `http://localhost:8080`.

**Option B: Run in a container**

```bash
podman build -t terminal-server .
podman run -d -p 5000:5000 --name terminal-server \
  --security-opt label=disable \
  -v /run/user/1000/podman/podman.sock:/var/run/docker.sock \
  -e TTY_LOGGING_ENABLED=true \
  -e MAX_CONTAINERS=30 \
  terminal-server
```

Server starts on `http://localhost:5000`.

Note: `--security-opt label=disable` is needed on SELinux systems (Fedora, RHEL) for the container to access the Podman socket.

### 3. Open the landing page

Navigate to the server URL. Pick a course, get a terminal. When you exit the shell, you're redirected back to the course selector.

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
| `PORT` | `8080` | Server listen port |

## Useful Commands

Remove all terminal containers:

```bash
podman rm -f $(podman ps -a --filter "label=app=web-terminal" -q)
```

List unique IP addresses from logs:

```bash
grep -hoP 'Origin IP: \K[\d\.]+' logs/*.log | sort -u
```
