#!/usr/bin/env bash
# Kind control-plane + KWOK workers + a production-shaped workload.
# Pods are scheduled by the default kube-scheduler (LeastAllocated) at a
# peak replica count, then scaled back to steady. Survivors are not
# rescheduled, so the holes from the scale-down stay in place.
set -euo pipefail

CLUSTER_CONFIG_FILE="./kind-config.yaml"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.20.2}"
CERT_MANAGER_URL="https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
ROOT="$(cd "$(dirname "$0")" && pwd)"

cd "$ROOT"

if [[ ! -f "$CLUSTER_CONFIG_FILE" ]]; then
    echo "Error: $CLUSTER_CONFIG_FILE not found."
    exit 1
fi

CLUSTER_NAME="$(awk '/^name:/{print $2; exit}' "$CLUSTER_CONFIG_FILE")"

if [[ -z "$CLUSTER_NAME" ]]; then
    echo "Error: $CLUSTER_CONFIG_FILE has no cluster name."
    exit 1
fi

# Only the local lab clusters. Never the kubeconfig context's cluster.
for existing in kind kwok; do
    if kind get clusters | grep -q "^${existing}$"; then
        echo "Cluster $existing already exists. Deleting..."
        kind delete cluster --name "$existing"
    fi
done

kind create cluster --config "$CLUSTER_CONFIG_FILE"
kubectl cluster-info --context "kind-${CLUSTER_NAME}"

echo "Waiting for Kind nodes..."
kubectl wait --for=condition=Ready nodes --all --timeout=120s

echo "Installing cert-manager ${CERT_MANAGER_VERSION}..."
kubectl apply -f "$CERT_MANAGER_URL"
kubectl wait --for=condition=Available deployment/cert-manager-webhook \
    -n cert-manager --timeout=5m
kubectl wait --for=condition=Available deployment/cert-manager \
    -n cert-manager --timeout=5m
kubectl wait --for=condition=Available deployment/cert-manager-cainjector \
    -n cert-manager --timeout=5m

echo "Creating self-signed ClusterIssuer..."
kubectl apply -f manifests/cert-manager-selfsigned-issuer.yaml

echo "Installing KWOK..."
helm repo add kwok https://kwok.sigs.k8s.io/charts/
helm repo update kwok
# Separate releases: installing stage-fast under the same name replaces the controller.
helm upgrade --install kwok kwok/kwok --namespace kube-system --wait --timeout 5m
helm upgrade --install kwok-stage kwok/stage-fast --namespace kube-system --timeout 5m

echo "Creating KWOK nodes..."
helm upgrade --install kwok-nodes charts/kwok-nodes --namespace kube-system
# Helm does not write Node status, so capacity and Ready are applied separately.
helm template kwok-nodes charts/kwok-nodes | kubectl apply --server-side --subresource=status -f -

echo "Waiting for KWOK nodes..."
kubectl wait --for=condition=Ready node -l type=kwok --timeout=180s

wait_for_benchmark_pods() {
    echo "Waiting for benchmark workloads..."
    kubectl wait --for=condition=Available deployment -l podtetris.io/benchmark=true -A --timeout=180s
    while read -r ns name _; do
        kubectl rollout status "statefulset/$name" -n "$ns" --timeout=180s
    done < <(kubectl get sts -A -l podtetris.io/benchmark=true --no-headers)
    kubectl rollout status daemonset/node-exporter -n monitoring --timeout=180s
}

echo "Scheduling the peak workload (LeastAllocated spread)..."
helm upgrade --install benchmark-workloads charts/workloads --set replicaMode=peak
wait_for_benchmark_pods

echo "Scaling back to steady. Existing pods stay on their nodes..."
helm upgrade --install benchmark-workloads charts/workloads --set replicaMode=steady
wait_for_benchmark_pods

echo "Cluster setup completed."
echo "Install PODTetris from the repo root:"
echo "  make deploy-local-kwok"
echo "Benchmark the planner:"
echo "  RESULTS_DIR=benchmarks/kind/kwok ./scripts/run-experiment.sh --planner-only --wait 600"
