#!/bin/bash

# Exit immediately if a command exits with a non-zero status
set -e

### KIND
CLUSTER_NAME="kind"
CLUSTER_CONFIG_FILE="./kind-config.yaml"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.20.2}"
CERT_MANAGER_URL="https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"

# Google Online Boutique (microservices-demo). Pin for reproducibility.
BOUTIQUE_VERSION="${BOUTIQUE_VERSION:-v0.10.7}"
BOUTIQUE_MANIFESTS_URL="https://raw.githubusercontent.com/GoogleCloudPlatform/microservices-demo/${BOUTIQUE_VERSION}/release/kubernetes-manifests.yaml"

# Extra replicas so packing / consolidation has something to work with.
# Stock boutique is 1 replica per service (~11 pods).
SCALE_REPLICAS="${SCALE_REPLICAS:-3}"

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

### Online Boutique (microservices-demo)
echo "Deploying Online Boutique ${BOUTIQUE_VERSION}..."
kubectl apply -f "$BOUTIQUE_MANIFESTS_URL"

# Kind has no cloud LoadBalancer; ClusterIP frontend + port-forward is enough.
echo "Removing frontend-external LoadBalancer (unsupported on Kind)..."
kubectl delete svc frontend-external --ignore-not-found

echo "Applying PodDisruptionBudgets..."
kubectl apply -f manifests/poddisruptionbudgets.yaml

if [[ "$SCALE_REPLICAS" -gt 1 ]]; then
    echo "Scaling selected boutique services to ${SCALE_REPLICAS} replicas..."
    kubectl scale deployment \
      frontend productcatalogservice currencyservice recommendationservice \
      checkoutservice cartservice adservice \
      --replicas="$SCALE_REPLICAS"
fi

echo "Waiting for Online Boutique deployments..."
kubectl wait --for=condition=Available deployment --all -n default --timeout=10m

echo "Cluster setup completed."
echo "Browse the shop with:"
echo "  kubectl port-forward svc/frontend 8080:80"
echo "  open http://localhost:8080"
echo ""
echo "When installing PODTetris, use the Kind + boutique overrides:"
echo "  helm upgrade --install podtetris ../../charts/podtetris -n podtetris --create-namespace \\"
echo "    -f values-kind.yaml -f values-boutique-rules.yaml"
echo "Or from repo root: make deploy-local-boutique"
