#!/bin/bash

# Exit immediately if a command exits with a non-zero status
set -e

### KIND
CLUSTER_NAME="kind"
CLUSTER_CONFIG_FILE="./kind-config.yaml"

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

### KWOK ###
 
#helm repo add kwok https://kwok.sigs.k8s.io/charts/
#helm upgrade --namespace kube-system --install kwok kwok/kwok
#helm upgrade --install kwok kwok/stage-fast
#helm upgrade --install kwok kwok/metrics-usage

## Create fake kwok nodes
#helm install kwok-nodes-provisioner charts/kwok-nodes-provisioner/

#kubectl apply -f manifests/databases.yaml
#kubectl apply -f manifests/fake-backups.yaml
#kubectl apply -f manifests/web-servers.yaml
kubectl apply -f manifests/podtetris-test-workloads.yaml

echo "Cluster setup completed."