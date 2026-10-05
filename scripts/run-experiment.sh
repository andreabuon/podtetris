#!/usr/bin/env bash
# Run planner against the current kubeconfig and save results under results/<timestamp>/.
# Cluster setup and chart deploy must be done separately.
#
# Usage:
#   ./scripts/run-experiment.sh
#   ./scripts/run-experiment.sh --wait 300

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NS=podtetris
RESULTS="$ROOT/results"
WAIT=180

while [[ $# -gt 0 ]]; do
  case "$1" in
    --wait) WAIT="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,10p' "$0"
      exit 0
      ;;
    *) echo "error: unknown option: $1" >&2; exit 1 ;;
  esac
done

cd "$ROOT"

NAME="$(date +%Y%m%d-%H%M%S)"
OUT="$RESULTS/$NAME"
mkdir -p "$OUT"
echo "Results directory: $OUT"

echo "Capturing pod state (before)..."
kubectl get pods -n default -o wide >"$OUT/pods-before.txt"

JOB="podtetris-planner-$NAME"
echo "Starting planner job: $JOB"
kubectl create job --from=cronjob/podtetris-planner "$JOB" -n "$NS"

echo "Waiting ${WAIT}s for consolidation to settle..."
sleep "$WAIT"

echo "Collecting artifacts..."
kubectl get pods -n default -o wide >"$OUT/pods-after.txt"
kubectl get podmoves -n "$NS" >"$OUT/podmoves.txt"
kubectl get podmoves -n "$NS" -o yaml >"$OUT/podmoves.yaml"
kubectl get consolidationplans -A -o yaml >"$OUT/plan.yaml" || true

kubectl logs -n "$NS" -l app.kubernetes.io/component=evictor --tail=-1 >"$OUT/evictor.txt" || true
kubectl logs -n "$NS" -l app=podtetris-webhook --tail=-1 >"$OUT/webhook.txt" || true
kubectl logs -n "$NS" -l "job-name=$JOB" --tail=-1 >"$OUT/planner.txt" || true

echo "Experiment complete: $OUT"
ls -la "$OUT"
