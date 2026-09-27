#!/usr/bin/env bash
set -euo pipefail

# Synthetic sensor load test runner for canary deployments
TARGET_URL="${TARGET_URL:-http://localhost:8080/api/v1/telemetry}"
VIRTUAL_DEVICES="${VIRTUAL_DEVICES:-1000}"
INJECT_ANOMALIES="${INJECT_ANOMALIES:-false}"
INJECT_LATENCY="${INJECT_LATENCY:-false}"
INJECT_MALFORMED_RATE="${INJECT_MALFORMED_RATE:-0.0}"

echo "=========================================================="
echo " Starting k6 Canary Sensor Telemetry Simulator"
echo " Target API:        $TARGET_URL"
echo " Virtual Devices:   $VIRTUAL_DEVICES"
echo " Inject Anomalies:  $INJECT_ANOMALIES"
echo " Inject Latency:    $INJECT_LATENCY"
echo " Malformed Rate:    $INJECT_MALFORMED_RATE"
echo "=========================================================="

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if command -v k6 &> /dev/null; then
  TARGET_URL="$TARGET_URL" \
  VIRTUAL_DEVICES="$VIRTUAL_DEVICES" \
  INJECT_ANOMALIES="$INJECT_ANOMALIES" \
  INJECT_LATENCY="$INJECT_LATENCY" \
  INJECT_MALFORMED_RATE="$INJECT_MALFORMED_RATE" \
  k6 run "$SCRIPT_DIR/scenarios/canary_telemetry.js"
else
  echo "k6 binary not found locally. Running via Docker container (grafana/k6)..."
  docker run --rm -i \
    -e TARGET_URL="$TARGET_URL" \
    -e VIRTUAL_DEVICES="$VIRTUAL_DEVICES" \
    -e INJECT_ANOMALIES="$INJECT_ANOMALIES" \
    -e INJECT_LATENCY="$INJECT_LATENCY" \
    -e INJECT_MALFORMED_RATE="$INJECT_MALFORMED_RATE" \
    --network host \
    -v "$SCRIPT_DIR:/scripts" \
    grafana/k6 run /scripts/scenarios/canary_telemetry.js
fi
