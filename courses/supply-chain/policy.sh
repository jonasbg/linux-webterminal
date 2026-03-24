#!/usr/local/bin/sh
set -eu

if [ $# -ne 1 ]; then
  echo "usage: $0 <image>" >&2
  exit 1
fi

IMAGE="$1"
OUT="/tmp/trivy-policy.json"

echo "Scanning $IMAGE"
trivy image --quiet --format json --output "$OUT" "$IMAGE"

CRITICAL=$(jq '[.Results[]?.Vulnerabilities[]? | select(.Severity == "CRITICAL")] | length' "$OUT")
HIGH=$(jq '[.Results[]?.Vulnerabilities[]? | select(.Severity == "HIGH")] | length' "$OUT")

echo "CRITICAL=$CRITICAL HIGH=$HIGH"

if [ "$CRITICAL" -gt 0 ]; then
  echo "Gate failed: critical vulnerabilities present"
  exit 1
fi

echo "Gate passed"
