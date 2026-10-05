#!/usr/bin/env bash
# Run planner against the current kubeconfig and save results under
# benchmarks/<timestamp>/. Cluster setup and chart deploy must be done separately.
#
# Usage:
#   ./scripts/run-experiment.sh
#   ./scripts/run-experiment.sh --wait 300
#   ./scripts/run-experiment.sh --planner-only
#
# Always saved: planner config, cluster name, cluster-autoscaler config,
# node resource requests (before), podtetris rules, planner logs,
# consolidation plan, podmoves.
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

while [[ $# -gt 0 ]]; do
  case "$1" in
    --wait) WAIT="$2"; shift 2 ;;
    --ns) NS="$2"; shift 2 ;;
    --planner-only) PLANNER_ONLY=1; shift ;;
    -h|--help)
      sed -n '2,20p' "$0"
      exit 0
      ;;
    *) echo "error: unknown option: $1" >&2; exit 1 ;;
  esac
done

cd "$ROOT"

NAME="$(date +%Y%m%d-%H%M%S)"
OUT="$RESULTS/$NAME"
mkdir -p "$OUT"
echo "Benchmark directory: $OUT"

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
CLUSTER="${kubectl config current-context 2>/dev/null}"
CLUSTER="${s##*/}"
echo $CLUSTER > "$OUT/cluster-name.txt"

echo "Saving planner config and rules..."
dump_cm_key "podtetris-config" 'config\.yaml' "$OUT/planner-config.yaml"
dump_cm_key "podtetris-scheduler-config" 'podtetris-scheduler-config\.yaml' "$OUT/planner-scheduler-config.yaml"
dump_cm_key "podtetris-planner-rules" 'rules\.yaml' "$OUT/planner-rules.yaml"

echo "Saving cluster-autoscaler config..."
kubectl get deploy -n kube-sytem autoscaler-aws-cluster-autoscaler -o yaml >"$OUT/cluster-autoscaler.yaml" | grep -i "containers" -A 30

echo "Capturing node resource requests (before)..."
#kubectl get nodes > "$OUT/nodes-before.txt"
kubectl top nodes > "$OUT/top-nodes-before.txt"

echo "Capturing pod state (before)..."
#kubectl get pods -n default -o wide >"$OUT/pods-before.txt"
kubectl get pods -A -o wide >"$OUT/pods-before.txt"

JOB="podtetris-planner-$NAME"
echo "Starting planner job: $JOB"
kubectl create job --from=cronjob/podtetris-planner "$JOB" -n "$NS"

if [[ "$PLANNER_ONLY" -eq 1 ]]; then
  echo "Waiting up to ${WAIT}s for planner job to finish..."
  kubectl wait --for=condition=complete "job/$JOB" -n "$NS" --timeout="${WAIT}s" \
    || echo "warning: planner job did not complete within ${WAIT}s; collecting artifacts anyway" >&2
else
  echo "Waiting ${WAIT}s for consolidation to settle..."
  sleep "$WAIT"
fi

echo "Collecting planner artifacts..."
kubectl get podmoves -n "$NS" >"$OUT/podmoves.txt"
kubectl get podmoves -n "$NS" -o yaml >"$OUT/podmoves.yaml"
kubectl get consolidationplans -A -o yaml >"$OUT/plan.yaml"
kubectl logs -n "$NS" -l "job-name=$JOB" --tail=-1 >"$OUT/planner.txt" || true

if [[ "$PLANNER_ONLY" -eq 0 ]]; then
  echo "Collecting evictor/webhook artifacts..."
  #kubectl get pods -n default -o wide >"$OUT/pods-after.txt"
  kubectl get pods -A -o wide >"$OUT/pods-after.txt"
  kubectl logs -n "$NS" -l app.kubernetes.io/component=evictor --tail=-1 >"$OUT/evictor.txt" || true
  kubectl logs -n "$NS" -l app=podtetris-webhook --tail=-1 >"$OUT/webhook.txt" || true
fi

echo "Experiment complete: $OUT"
ls -la "$OUT"
