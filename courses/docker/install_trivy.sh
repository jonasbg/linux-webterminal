#!/bin/sh
set -e

echo "Finding latest Trivy release..."
LATEST_VERSION=$(curl -s https://api.github.com/repos/aquasecurity/trivy/releases/latest | grep "tag_name" | cut -d '"' -f 4)
echo "Latest Trivy version: $LATEST_VERSION"

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  TRIVY_ARCH="64bit" ;;
    aarch64) TRIVY_ARCH="ARM64" ;;
    armv7l)  TRIVY_ARCH="ARM" ;;
    *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

DOWNLOAD_URL="https://github.com/aquasecurity/trivy/releases/download/${LATEST_VERSION}/trivy_${LATEST_VERSION#v}_Linux-${TRIVY_ARCH}.tar.gz"

echo "Downloading from: $DOWNLOAD_URL"
mkdir -p /tmp/trivy
cd /tmp/trivy
curl -sSL "$DOWNLOAD_URL" -o trivy.tar.gz
tar -xzf trivy.tar.gz

cp trivy /usr/local/bin/
chmod +x /usr/local/bin/trivy

cd /
rm -rf /tmp/trivy
echo "Trivy installation complete: $LATEST_VERSION ($TRIVY_ARCH)"
