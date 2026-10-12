#!/usr/bin/env bash
# Apply every file in evaluation/planner/, then record the planner run.
# The planner CronJob must already be installed. --reuse-values keeps that release.
#
# Usage:
#   ./scripts/run-evaluation.sh --ns <namespace>
#   ./scripts/run-evaluation.sh --ns <namespace> --release <helm-release>

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NS=podtetris
RELEASE=podtetris
CONFIG_DIR="$ROOT/evaluation/planner"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ns) NS="$2"; shift 2 ;;
    --release) RELEASE="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,7p' "$0"
      exit 0
      ;;
    *) echo "error: unknown option: $1" >&2; exit 1 ;;
  esac
done

cd "$ROOT"

for config in "$CONFIG_DIR"/*.yaml; do
  kubectl delete consolidationplans -n "$NS" --all
  echo "Applying $(basename "$config")"
  helm upgrade "$RELEASE" charts/podtetris -n "$NS" \
    --reuse-values \
    -f "$CONFIG_DIR/baseline.yaml" \
    -f "$config"
  ./scripts/run-experiment.sh --planner-only --ns "$NS"
done

kubectl delete consolidationplans -n "$NS" --all