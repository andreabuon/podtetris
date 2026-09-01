#!/usr/bin/env bash
# Run planner against the current kubeconfig and save results under results/testN/.
# Cluster setup must be done separately
#
# Usage:
#   ./scripts/run-experiment.sh              # deploy-local + planner + collect
#   ./scripts/run-experiment.sh --aws        # deploy (no kind values) + planner + collect
#   ./scripts/run-experiment.sh --skip-deploy
#   ./scripts/run-experiment.sh --name test10 --wait 300

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NS=podtetris
RESULTS="$ROOT/results"
WAIT=300
DEPLOY=local   # local | aws
SKIP_DEPLOY=0
NAME=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --aws)         DEPLOY=aws; shift ;;
    --local)       DEPLOY=local; shift ;;
    --skip-deploy) SKIP_DEPLOY=1; shift ;;
    --wait)        WAIT="$2"; shift 2 ;;
    --name)        NAME="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,12p' "$0"
      exit 0
      ;;
    *) echo "error: unknown option: $1" >&2; exit 1 ;;
  esac
done

cd "$ROOT"

# --- results dir: next testN unless --name given ---
if [[ -z "$NAME" ]]; then
  max=0
  for d in "$RESULTS"/test[0-9]*; do
    [[ -d "$d" ]] || continue
    n="${d##*/test}"
    if [[ "$n" =~ ^[0-9]+$ ]] && (( n > max )); then
      max=$n
    fi
  done
  NAME="test$((max + 1))"
fi

OUT="$RESULTS/$NAME"
[[ -e "$OUT" ]] && { echo "error: results directory already exists: $OUT" >&2; exit 1; }
mkdir -p "$OUT"
echo "Results directory: $OUT"

# --- deploy (optional) ---
if (( SKIP_DEPLOY == 0 )); then
  if [[ "$DEPLOY" == local ]]; then
    echo "Deploying chart (local)..."
    make deploy-local
  else
    echo "Deploying chart (aws)..."
    make deploy
  fi
  kubectl wait --for=condition=Available \
    deployment/podtetris-evictor deployment/podtetris-webhook \
    -n "$NS" --timeout=5m
fi

# --- before ---
echo "Capturing pod state (before)..."
kubectl get pods -n default -o wide >"$OUT/pods-before.txt"

# --- planner ---
JOB="podtetris-planner-$(date +%Y%m%d-%H%M%S)"
echo "Starting planner job: $JOB"
kubectl create job --from=cronjob/podtetris-planner "$JOB" -n "$NS"

echo "Waiting ${WAIT}s for consolidation to settle..."
sleep "$WAIT"

# --- after ---
echo "Collecting artifacts..."
kubectl get pods -n default -o wide >"$OUT/pods-after.txt"
kubectl get podmoves -n "$NS" -o yaml >"$OUT/podmoves.yaml"
kubectl get consolidationplans -A -o yaml >"$OUT/plan.yaml" || true

kubectl logs -n "$NS" -l app.kubernetes.io/component=evictor --tail=-1 >"$OUT/evictor.txt" || true
kubectl logs -n "$NS" -l app=podtetris-webhook --tail=-1 >"$OUT/webhook.txt" || true
# Job pods are labeled job-name=<job>; more reliable than logs job/<name> after completion.
kubectl logs -n "$NS" -l "job-name=$JOB" --tail=-1 >"$OUT/planner.txt" || true

echo "Experiment complete: $OUT"
ls -la "$OUT"
