# DevSecOps Lab

A secure web-based terminal platform that runs isolated containers per course/task. Users pick a task from a landing page and get their own sandboxed shell environment with an integrated guide sidebar.

## Courses

| Course | Description |
|--------|-------------|
| **Linux I** | clmystery command-line challenge |
| **Linux II** | Process investigation - /proc, PIDs, file descriptors |
| **Container Fundamentals** | Namespaces, cgroups, and the runtime stack |
| **Docker Workshop** | Podman-in-podman, multi-stage builds, Trivy, Hadolint |
| **Git Signing** | GPG/SSH commit signing and verification |
| **Supply Chain** | Image scanning, SBOMs, digests, and simple CI/CD policy gates |
| **Kubernetes Basics** | Real kubectl against a mock API server |
| **Kubernetes Networking** | Pods, Services, Gateway API, and NetworkPolicy with a Cilium-focused dataplane model |

## Running locally

### Prerequisites

- Podman (or Docker) with a running socket
- Python 3.12+

### 1. Build course images

```bash
cd courses
podman build -t git.torden.tech/jonasbg/terminal-linux-1:latest -f linux-1/Dockerfile linux-1/
podman build -t git.torden.tech/jonasbg/terminal-linux-2:latest -f linux-2/Dockerfile linux-2/
podman build -t git.torden.tech/jonasbg/terminal-containers:latest -f containers/Dockerfile containers/
podman build -t git.torden.tech/jonasbg/terminal-docker:latest -f docker/Dockerfile docker/
podman build -t git.torden.tech/jonasbg/terminal-git-signing:latest -f git-signing/Dockerfile git-signing/
podman build -t git.torden.tech/jonasbg/terminal-supply-chain:latest -f supply-chain/Dockerfile supply-chain/
podman build -t git.torden.tech/jonasbg/terminal-kubernetes:latest -f kubernetes/Dockerfile kubernetes/
podman build -t git.torden.tech/jonasbg/terminal-kubernetes-networking:latest -f kubernetes-networking/Dockerfile .
```

Or with Docker Compose:

```bash
cd courses
docker compose build
```

Or with the deployment helper from the repo root:

```bash
./deploy.sh
```

### 2. Run the server

**Development:**

```bash
pip install -r requirements-lock.txt
python app.py
```

**Container:**

```bash
podman build -t terminal-server .
podman run -d -p 5000:5000 --name terminal-server \
  --security-opt label=disable \
  -v /run/user/1000/podman/podman.sock:/var/run/docker.sock \
  -e TTY_LOGGING_ENABLED=true \
  -e MAX_CONTAINERS=30 \
  terminal-server
```

Note: `--security-opt label=disable` is needed on SELinux systems (Fedora, RHEL).

### 3. Deploy to remote

```bash
ssh containeruser@10.10.10.168 ~/deploy.sh
```

## Adding course content

### Guide files

Guide files are served from inside the container images. Add paths to the `guides` list in `app.py`:

```python
'guides': ['/home/termuser/instruction.md', '/home/termuser/cheatsheet.md'],
```

The terminal page shows a "Guide" sidebar with tabs for each file. Content is extracted from the container image on first request and cached.

### Images in guides

Place images in `courses/<slug>/images/`:

```
courses/kubernetes/images/architecture.png
```

Reference in any guide or README markdown:

```markdown
![Architecture](/api/courses/kubernetes/images/architecture.png)
```

Images are served directly by the server (not from containers). They render with rounded corners in both the guide sidebar and the landing page preview dialog.

## Security Profiles

These are internal runtime profiles used by the platform. They are not shown in the user-facing course cards.

**Strict** (Linux I, Linux II, Container Fundamentals, Git Signing, Kubernetes):
- No network access
- Read-only filesystem (64MB tmpfs for /home and /tmp)
- All capabilities dropped, no-new-privileges
- 64MB memory, 10% CPU, 10 PIDs (configurable per course)
- /proc entries masked

**Builder** (Docker Workshop, Supply Chain):
- Bridge networking (pull images from registries)
- Writable filesystem
- Privileged mode for nested Podman builds
- Avoids `userns_mode=host`
- 768MB memory, 50% CPU, 256 PIDs

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HOST` | `*` | CORS allowed origins |
| `TTY_LOGGING_ENABLED` | `false` | Enable terminal session logging |
| `TTY_LOG_DIR` | `./logs` | Log directory |
| `MAX_CONTAINERS` | `10` | Max concurrent containers |
| `CONTAINER_LIFETIME` | `3600` | Auto-cleanup timeout (seconds) |
| `PORT` | `8080` | Server listen port |
