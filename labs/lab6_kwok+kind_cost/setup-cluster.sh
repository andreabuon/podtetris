#!/bin/bash

# Exit immediately if a command exits with a non-zero status
set -e

### KIND
CLUSTER_NAME="kind"
CLUSTER_CONFIG_FILE="./kind-config.yaml"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.20.2}"
CERT_MANAGER_URL="https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"

# Check for config file existence
if [[ ! -f "$CLUSTER_CONFIG_FILE" ]]; then
    echo "Error: $CLUSTER_CONFIG_FILE not found."
    exit 1
fi

# Check if a cluster already exists. If so, delete it
if kind get clusters | grep -q "^$CLUSTER_NAME$"; then
    echo "Cluster $CLUSTER_NAME already exists. Deleting..."
    kind delete cluster --name "$CLUSTER_NAME"
fi

# Create the cluster
kind create cluster --config "$CLUSTER_CONFIG_FILE"

# Set the context to the new cluster
kubectl cluster-info --context kind-$CLUSTER_NAME

# Wait for the nodes to be ready
echo "Waiting for the nodes to be ready..."
kubectl wait --for=condition=Ready nodes --all --timeout=60s

### cert-manager (required by PODTetris mutating webhook TLS)
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

kubectl apply -f manifests/podtetris-test-workloads.yaml

echo "Cluster setup completed."
echo "When installing PODTetris, use the Kind issuer overrides:"
echo "  helm upgrade --install podtetris ../../charts/podtetris -n podtetris --create-namespace -f values-kind.yaml"
