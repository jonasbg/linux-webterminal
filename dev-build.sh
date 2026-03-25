#!/bin/bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER_IMAGE="localhost/terminal-server:latest"
SERVER_CONTAINER="terminal-server-local"
LOCAL_COURSE_CONFIG="${REPO_DIR}/course-images.local.yaml"
COURSES=(
    linux-1
    linux-2
    containers
    docker
    git-signing
    supply-chain
    kubernetes
    kubernetes-networking
)

echo "=== Building server ==="
cd "${REPO_DIR}"
podman build -q -t "${SERVER_IMAGE}" -f Dockerfile .

echo "=== Writing local course overrides ==="
: > "${LOCAL_COURSE_CONFIG}"

for course in "${COURSES[@]}"; do
    image="localhost/terminal-${course}:latest"
    remote_image="git.torden.tech/jonasbg/terminal-${course}:latest"

    echo "=== Building ${course} ==="
    if [ "${course}" = "kubernetes-networking" ]; then
        podman build -q -t "${image}" -f "courses/${course}/Dockerfile" "courses/"
    else
        podman build -q -t "${image}" -f "courses/${course}/Dockerfile" "courses/${course}/"
    fi

    echo "=== Tagging ${course} ==="
    podman tag "${image}" "${remote_image}"

    cat >> "${LOCAL_COURSE_CONFIG}" <<EOF
${course}:
  image: ${image}
EOF
done

echo "=== Replacing local server ==="
podman rm -f "${SERVER_CONTAINER}" >/dev/null 2>&1 || true

podman run -d \
    --name "${SERVER_CONTAINER}" \
    -p 5000:5000 \
    --security-opt label=disable \
    -v "/run/user/$(id -u)/podman/podman.sock:/var/run/docker.sock" \
    -v "${LOCAL_COURSE_CONFIG}:/app/course-images.local.yaml:ro" \
    -e TTY_LOGGING_ENABLED=true \
    -e MAX_CONTAINERS=30 \
    -e COURSES_PATHS=/app/courses.yaml:/app/course-images.local.yaml \
    "${SERVER_IMAGE}"

echo "=== Done ==="
echo "Local server: http://127.0.0.1:5000"
podman ps --filter "name=${SERVER_CONTAINER}" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
