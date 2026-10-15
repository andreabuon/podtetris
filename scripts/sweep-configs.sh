#!/usr/bin/env bash
# Apply every file in evaluation/planner/, then record the planner run.
# Results land under benchmarks/<run-id>/<config-name>/.
# A summary.csv is written in the run directory from the planner "Run summary" log line
# (outcome, best plan, simulation counts, planner phase timings), plus job_seconds from
# the Job status and total_seconds for the whole config step including helm upgrade.
# result is "fatal" if the planner crashed, "missing" if there is no summary line.
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
      sed -n '2,13p' "$0"
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
  config_start=$SECONDS
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
  timing="$RUN_DIR/$config_name/timing.json"
  total_seconds=$((SECONDS - config_start))
  if [[ -f "$timing" ]]; then
    jq --argjson total "$total_seconds" '. + {total_seconds: $total}' "$timing" >"$timing.tmp" \
      && mv "$timing.tmp" "$timing"
  else
    jq -n --argjson total "$total_seconds" '{total_seconds: $total}' >"$timing"
  fi
  echo "Finished $config_name in ${total_seconds}s"
done

kubectl delete consolidationplans,podmoves -n "$NS" --all --ignore-not-found --wait=true >/dev/null

# Runs results Summary
SUMMARY="$RUN_DIR/summary.csv"
echo "run_id,config,result,strategy,freed_nodes,moves,cost,score,worker_nodes,pods,candidate_nodes,candidate_sets,evicted_pods,simulations,simulations_failed,simulations_with_freed_nodes,cache_sync_seconds,simulation_seconds,apply_seconds,planner_seconds,job_seconds,total_seconds" >"$SUMMARY"

# Columns taken from the planner "Run summary" log line, in header order (result .. planner_seconds).
SUMMARY_FIELDS='def ms: if . == null then null else . * 1000 | round / 1000 end;
  [.outcome, .strategy, .freedNodes, .moves, .cost, .score,
   .workerNodes, .pods, .candidateNodes, .candidateSets, .evictedPods,
   .simulations, .simulationsFailed, .simulationsWithFreedNodes,
   (.cacheSyncSeconds | ms), (.simulationSeconds | ms), (.applySeconds | ms), (.totalSeconds | ms)]
  | map(if . == null then "" else tostring end) | join(",")'
EMPTY_FIELDS="$(printf ',%.0s' {1..17})"

for config_dir in "$RUN_DIR"/*/; do
  [[ -d "$config_dir" ]] || continue
  config_name="$(basename "$config_dir")"
  planner="$config_dir/planner.log"
  job_seconds=""
  total_seconds=""

  timing="$config_dir/timing.json"
  if [[ -f "$timing" ]]; then
    job_seconds="$(jq -r '.job_seconds // empty' "$timing")"
    total_seconds="$(jq -r '.total_seconds // empty' "$timing")"
  fi

  run_summary=""
  if [[ -f "$planner" ]]; then
    # Only the zap JSON lines are parsed; klog lines are skipped.
    run_summary="$( (grep '^{' "$planner" || true) \
      | jq -c 'select(.msg == "Run summary")' 2>/dev/null | tail -n 1 || true)"
  fi

  if [[ -n "$run_summary" ]]; then
    planner_fields="$(jq -r "$SUMMARY_FIELDS" <<<"$run_summary")"
  elif [[ -f "$planner" ]] && grep -q '"level":"fatal"' "$planner"; then
    planner_fields="fatal$EMPTY_FIELDS"
  else
    planner_fields="missing$EMPTY_FIELDS"
  fi

  echo "$RUN_ID,$config_name,$planner_fields,$job_seconds,$total_seconds" >>"$SUMMARY"
done

echo "Evaluation complete: $RUN_DIR"
