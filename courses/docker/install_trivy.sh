#!/bin/sh
set -e

echo "Finding latest Trivy release..."
# Get latest release tag from GitHub API
LATEST_VERSION=$(curl -s https://api.github.com/repos/aquasecurity/trivy/releases/latest | grep "tag_name" | cut -d '"' -f 4)

echo "Latest Trivy version: $LATEST_VERSION"

# Download URL for Linux x86_64
DOWNLOAD_URL="https://github.com/aquasecurity/trivy/releases/download/${LATEST_VERSION}/trivy_${LATEST_VERSION#v}_Linux-64bit.tar.gz"

echo "Downloading from: $DOWNLOAD_URL"
# Create temp directory
mkdir -p /tmp/trivy
cd /tmp/trivy

# Download and extract
curl -sSL $DOWNLOAD_URL -o trivy.tar.gz
tar -xzf trivy.tar.gz

# Install to /usr/local/bin
echo "Installing Trivy to /usr/local/bin/"
cp trivy /usr/local/bin/
chmod +x /usr/local/bin/trivy

# Clean up
cd /
rm -rf /tmp/trivy

echo "Trivy installation complete. Version: $LATEST_VERSION"