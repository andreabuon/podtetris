#!/usr/bin/env bash
# Apply every file in evaluation/planner/, then record the planner run.
# Results land under benchmarks/<run-id>/<config-name>/.
# A summary.csv is written in the run directory.
# The planner CronJob must already be installed. Its values are captured once at the start
# and layered under baseline.yaml + the config, so configs never inherit from each other.
#
# Usage:
#   ./scripts/sweep-configs.sh --ns <namespace>
#   ./scripts/sweep-configs.sh --ns <namespace> --release <helm-release>

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
      sed -n '2,10p' "$0"
      exit 0
      ;;
    *) echo "error: unknown option: $1" >&2; exit 1 ;;
  esac
done

cd "$ROOT"

RUN_ID="$(date +%Y%m%d-%H%M%S)"
RUN_DIR="$RESULTS/$RUN_ID"
mkdir -p "$RUN_DIR"
echo "Sweep directory: $RUN_DIR"

install_values="$RUN_DIR/install-values.yaml"
helm get values "$RELEASE" -n "$NS" -o yaml >"$install_values"

for config in "$CONFIG_DIR"/*.yaml; do
  config_name="$(basename "$config" .yaml)"
  kubectl delete consolidationplans -n "$NS" --all --ignore-not-found >/dev/null
  echo "Applying $config_name"
  helm_out="$(mktemp)"
  if ! helm upgrade "$RELEASE" charts/podtetris -n "$NS" \
    -f "$install_values" \
    -f "$CONFIG_DIR/baseline.yaml" \
    -f "$config" >"$helm_out" 2>&1; then
    echo "error: helm upgrade failed for $config_name" >&2
    cat "$helm_out" >&2
    rm -f "$helm_out"
    exit 1
  fi
  rm -f "$helm_out"
  ./scripts/record-run.sh --planner-only --ns "$NS" \
    --run-id "$RUN_ID" \
    --config-name "$config_name"
  kubectl delete job "planner-${RUN_ID}-${config_name}" -n "$NS" --ignore-not-found >/dev/null
done

kubectl delete consolidationplans -n "$NS" --all --ignore-not-found >/dev/null

# Runs results Summary
SUMMARY="$RUN_DIR/summary.csv"
echo "run_id,config,strategy,freed_nodes,moves,cost,score,result" >"$SUMMARY"

for config_dir in "$RUN_DIR"/*/; do
  [[ -d "$config_dir" ]] || continue
  config_name="$(basename "$config_dir")"
  planner="$config_dir/planner.log"
  strategy=""
  freed_nodes=""
  moves=""
  cost=""
  score=""
  result="missing"

  if [[ -f "$planner" ]]; then
    selected="$(grep "Selected best consolidation plan" "$planner" || true)"
    if [[ -n "$selected" ]]; then
      strategy="$(jq -r '.strategy' <<<"$selected")"
      freed_nodes="$(jq -r '.freedNodes' <<<"$selected")"
      moves="$(jq -r '.moves' <<<"$selected")"
      cost="$(jq -r '.cost' <<<"$selected")"
      score="$(jq -r '.score' <<<"$selected")"
      if grep -q "applying consolidation plan" "$planner"; then
        result="applied"
      elif grep -q "dry run is enabled" "$planner"; then
        result="dry-run"
      elif grep -q "skipping apply" "$planner"; then
        result="skipped"
      else
        result="selected"
      fi
    elif grep -q "No viable consolidation plan found" "$planner"; then
      result="none"
    fi
  fi

  echo "$RUN_ID,$config_name,$strategy,$freed_nodes,$moves,$cost,$score,$result" >>"$SUMMARY"
done

echo "Evaluation complete: $RUN_DIR"
