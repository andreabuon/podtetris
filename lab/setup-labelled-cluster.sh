#!/bin/bash

CLUSTER_NAME="kind"

# Check if a cluster named "kind" already exists
if kind get clusters | grep -q "^$CLUSTER_NAME$"; then
    echo "Cluster $CLUSTER_NAME already exists. Deleting..."
    kind delete cluster --name "$CLUSTER_NAME"
fi

# Create the cluster
kind create cluster --config ./kind-config.yaml

# Set the context to the new cluster
kubectl cluster-info --context kind-$CLUSTER_NAME

# Wait for the nodes to be ready
echo ""
echo "Waiting for the nodes to be ready..."
kubectl wait --for=condition=Ready nodes --all --timeout=60s

# Apply labels to the nodes
echo ""
echo "Applying labels to the nodes..."
## EU region
### Zone 1
kubectl label nodes $CLUSTER_NAME-worker disk=nvme zone=eu-1
kubectl label nodes $CLUSTER_NAME-worker2 disk=ssd zone=eu-1
kubectl label nodes $CLUSTER_NAME-worker3 disk=hdd zone=eu-1
### Zone 2
kubectl label nodes $CLUSTER_NAME-worker4 disk=nvme zone=eu-2
kubectl label nodes $CLUSTER_NAME-worker5 disk=ssd zone=eu-2
kubectl label nodes $CLUSTER_NAME-worker6 disk=hdd zone=eu-2

echo ""
echo "Cluster setup completed."