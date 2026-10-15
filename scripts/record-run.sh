#!/usr/bin/env bash
# Run planner against the current kubeconfig and save results under
# benchmarks/<run-id>/, or benchmarks/<run-id>/<config-name>/ when
# --config-name is set. Cluster setup and chart deploy must be done separately.
#
# Usage:
#   ./scripts/record-run.sh
#   ./scripts/record-run.sh --wait 300
#   ./scripts/record-run.sh --planner-only
#   ./scripts/record-run.sh --run-id 20261012-103400 --config-name percent-50
#
# Always saved: planner config, cluster name, cluster-autoscaler config,
# node resource requests (before), podtetris rules, planner logs,
# consolidation plan, podmoves, timing.json (planner job duration and
# wall-clock duration of this script).
# Cluster-autoscaler config is saved on non-kind clusters.
#
# Default also saves pods-before/after, evictor logs, and webhook logs.
# --planner-only skips those extra artifacts and waits for the planner job
# instead of sleeping for consolidation.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NS=podtetris
RESULTS="$ROOT/benchmarks"
WAIT=180
PLANNER_ONLY=0
RUN_ID=""
CONFIG_NAME=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --wait) WAIT="$2"; shift 2 ;;
    --ns) NS="$2"; shift 2 ;;
    --run-id) RUN_ID="$2"; shift 2 ;;
    --config-name) CONFIG_NAME="$2"; shift 2 ;;
    --planner-only) PLANNER_ONLY=1; shift ;;
    -h|--help)
      sed -n '2,20p' "$0"
      exit 0
      ;;
    *) echo "error: unknown option: $1" >&2; exit 1 ;;
  esac
done

cd "$ROOT"
SECONDS=0

if [[ -z "$RUN_ID" ]]; then
  RUN_ID="$(date +%Y%m%d-%H%M%S)"
fi
RESULTS_DIR="$RESULTS/$RUN_ID"
if [[ -n "$CONFIG_NAME" ]]; then
  RESULTS_DIR="$RESULTS_DIR/$CONFIG_NAME"
fi
mkdir -p "$RESULTS_DIR"
echo "Benchmark directory: $RESULTS_DIR"

dump_cm_key() {
  local name="$1" key="$2" dest="$3"
  if ! kubectl get configmap "$name" -n "$NS" >/dev/null 2>&1; then
    echo "# configmap $name not found in namespace $NS" >"$dest"
    return
  fi
  kubectl get configmap "$name" -n "$NS" -o "jsonpath={.data.${key}}" >"$dest"
  if [[ ! -s "$dest" ]]; then
    echo "# configmap $name has no data key ${key//\\/}" >"$dest"
  fi
}

echo "Saving cluster name..."
CONTEXT="$(kubectl config current-context)"
if [[ "$CONTEXT" == kind-* ]]; then
  CLUSTER="$CONTEXT"
else
  CLUSTER="${CONTEXT##*/}"
fi
echo "$CLUSTER" > "$RESULTS_DIR/cluster-name.txt"

echo "Saving planner config and rules..."
dump_cm_key "podtetris-config" 'config\.yaml' "$RESULTS_DIR/planner-config.yaml"
dump_cm_key "podtetris-scheduler-config" 'podtetris-scheduler-config\.yaml' "$RESULTS_DIR/planner-scheduler-config.yaml"
dump_cm_key "podtetris-planner-rules" 'rules\.yaml' "$RESULTS_DIR/planner-rules.yaml"

if [[ "$CONTEXT" != kind-* ]]; then
  echo "Saving cluster-autoscaler config..."
  if ! kubectl get deploy -n kube-system autoscaler-aws-cluster-autoscaler -o yaml >"$RESULTS_DIR/cluster-autoscaler.yaml"; then
    echo "# deploy autoscaler-aws-cluster-autoscaler not found in kube-system" >"$RESULTS_DIR/cluster-autoscaler.yaml"
  fi
fi

echo "Capturing node resource requests (before)..."
#kubectl get nodes > "$RESULTS_DIR/nodes-before.txt"
kubectl get nodes -o json >"$RESULTS_DIR/nodes-before.json"
kube-capacity >"$RESULTS_DIR/capacity-nodes-before.txt" 2>&1
kube-capacity -o json >"$RESULTS_DIR/capacity-nodes-before.json"

echo "Capturing pod state (before)..."
#kubectl get pods -n default -o wide >"$RESULTS_DIR/pods-before.txt"
kubectl get pods -A -o wide >"$RESULTS_DIR/pods-before.txt"
kubectl get pods -A -o json >"$RESULTS_DIR/pods-before.json"

echo "Deleting old ConsolidationPlans and PodMoves..."
kubectl delete consolidationplans,podmoves -n "$NS" --all --ignore-not-found --wait=true >/dev/null

if [[ -n "$CONFIG_NAME" ]]; then
  JOB="planner-${RUN_ID}-${CONFIG_NAME}"
else
  JOB="planner-${RUN_ID}"
fi
echo "Starting planner job: $JOB"
kubectl create job --from=cronjob/podtetris-planner "$JOB" -n "$NS" >/dev/null

if [[ "$PLANNER_ONLY" -eq 1 ]]; then
  echo "Waiting up to ${WAIT}s for planner job to finish..."
  kubectl wait --for=condition=complete "job/$JOB" -n "$NS" --timeout="${WAIT}s" >/dev/null \
    || echo "warning: planner job did not complete within ${WAIT}s; collecting artifacts anyway" >&2
else
  echo "Waiting ${WAIT}s for consolidation to settle..."
  sleep "$WAIT"
fi

echo "Collecting planner artifacts..."
kubectl get podmoves -n "$NS" >"$RESULTS_DIR/podmoves.txt"
kubectl get podmoves -n "$NS" -o yaml >"$RESULTS_DIR/podmoves.yaml"
kubectl get consolidationplans -A -o yaml >"$RESULTS_DIR/plan.yaml"
kubectl logs -n "$NS" -l "job-name=$JOB" --tail=-1 >"$RESULTS_DIR/planner.log" || true


if [[ "$PLANNER_ONLY" -eq 0 ]]; then
  kube-capacity >"$RESULTS_DIR/capacity-nodes-after.txt" 2>&1
  kube-capacity -o json >"$RESULTS_DIR/capacity-nodes-after.json" 2>&1

  echo "Collecting evictor/webhook artifacts..."
  
  kubectl get nodes > "$RESULTS_DIR/nodes-before.txt"
  kubectl get nodes -o json >"$RESULTS_DIR/nodes-before.json"

  #kubectl get pods -n default -o wide >"$RESULTS_DIR/pods-after.txt"
  kubectl get pods -A -o wide >"$RESULTS_DIR/pods-after.txt"
  kubectl get pods -A -o json >"$RESULTS_DIR/pods-after.json"

  kubectl logs -n "$NS" -l app.kubernetes.io/component=evictor --tail=-1 >"$RESULTS_DIR/controller.log" || true
  kubectl logs -n "$NS" -l app=podtetris-webhook --tail=-1 >"$RESULTS_DIR/webhook.log" || true
fi

echo "Saving timing..."
kubectl get job "$JOB" -n "$NS" -o json 2>/dev/null \
  | jq --argjson record_seconds "$SECONDS" '{
      job: .metadata.name,
      job_start: .status.startTime,
      job_completion: .status.completionTime,
      job_seconds: (if .status.startTime and .status.completionTime
        then (.status.completionTime | fromdate) - (.status.startTime | fromdate)
        else null end),
      record_seconds: $record_seconds
    }' >"$RESULTS_DIR/timing.json" \
  || jq -n --arg job "$JOB" --argjson record_seconds "$SECONDS" \
    '{job: $job, job_start: null, job_completion: null, job_seconds: null, record_seconds: $record_seconds}' \
    >"$RESULTS_DIR/timing.json"

echo "Run recorded: $RESULTS_DIR"
