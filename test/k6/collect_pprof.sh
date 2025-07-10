#!/usr/bin/env bash
# Collect a CPU profile, a heap snapshot, and a goroutine snapshot at peak load.

set -euo pipefail

# Environment variables
PPROF_HOST="${PPROF_HOST:-localhost:8081}"   # pprof HTTP endpoint
CPU_SECONDS="${CPU_SECONDS:-150}"            # CPU profile duration
PEAK_DELAY="${PEAK_DELAY:-90}"               # Seconds to wait before peak capture
OUT_DIR="${OUT_DIR:-profiles_$(date +%Y%m%d_%H%M%S)}"

mkdir -p "${OUT_DIR}"

echo "Capturing ${CPU_SECONDS}s CPU profile"
curl -s "http://${PPROF_HOST}/debug/pprof/profile?seconds=${CPU_SECONDS}" \
     -o "${OUT_DIR}/cpu_${CPU_SECONDS}s.pb.gz" &
CPU_PID=$!

sleep "${PEAK_DELAY}"

echo "Saving peak heap"
curl -s "http://${PPROF_HOST}/debug/pprof/heap" \
     -o "${OUT_DIR}/heap_peak.pb.gz"

echo "Saving peak goroutine snapshot"
curl -s "http://${PPROF_HOST}/debug/pprof/goroutine" \
     -o "${OUT_DIR}/goroutine_peak.pb.gz"

wait "${CPU_PID}"

echo "Profiles saved to ${OUT_DIR}"
