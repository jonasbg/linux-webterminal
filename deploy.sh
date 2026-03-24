#!/bin/bash
set -e

REPO_DIR="${HOME}/repo"
COURSES="linux-1 linux-2 containers docker git-signing supply-chain kubernetes kubernetes-cilium"

echo "=== Pulling latest ==="
podman run --rm -v "${REPO_DIR}:/repo:Z" docker.io/alpine/git -C /repo pull

echo "=== Building server ==="
cd "${REPO_DIR}"
podman build -q -t terminal-server -f Dockerfile .

for course in ${COURSES}; do
    echo "=== Building ${course} ==="
    if [ "${course}" = "kubernetes-cilium" ]; then
        podman build -q -t "terminal-${course}:latest" -f "courses/${course}/Dockerfile" "courses/"
    else
        podman build -q -t "terminal-${course}:latest" -f "courses/${course}/Dockerfile" "courses/${course}/"
    fi
    podman tag "localhost/terminal-${course}:latest" "git.torden.tech/jonasbg/terminal-${course}:latest"
done

echo "=== Restarting server ==="
systemctl --user restart terminal-server

echo "=== Done ==="
podman ps --format "table {{.Names}}\t{{.Status}}"
