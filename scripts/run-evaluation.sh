#!/usr/bin/env bash
# Apply every file in evaluation/planner/, then record the planner run.
# Results land under benchmarks/<run-id>/<config-name>/.
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
RESULTS="$ROOT/benchmarks"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ns) NS="$2"; shift 2 ;;
    --release) RELEASE="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,8p' "$0"
      exit 0
      ;;
    *) echo "error: unknown option: $1" >&2; exit 1 ;;
  esac
done

cd "$ROOT"

RUN_ID="$(date +%Y%m%d-%H%M%S)"
RUN_DIR="$RESULTS/$RUN_ID"
mkdir -p "$RUN_DIR"
echo "Evaluation run directory: $RUN_DIR"

for config in "$CONFIG_DIR"/*.yaml; do
  config_name="$(basename "$config" .yaml)"
  kubectl delete consolidationplans -n "$NS" --all
  echo "Applying $config_name"
  helm upgrade "$RELEASE" charts/podtetris -n "$NS" \
    --reuse-values \
    -f "$CONFIG_DIR/baseline.yaml" \
    -f "$config"
  ./scripts/run-experiment.sh --planner-only --ns "$NS" \
    --run-id "$RUN_ID" \
    --config-name "$config_name"
done

kubectl delete consolidationplans -n "$NS" --all
echo "Evaluation complete: $RUN_DIR"
