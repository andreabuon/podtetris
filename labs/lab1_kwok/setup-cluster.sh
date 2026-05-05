#!/bin/bash

# Exit immediately if a command exits with a non-zero status
set -e

# Check if a cluster already exists. If so, delete it
if kwokctl get clusters | grep -q "^kwok$"; then
    echo "Kwok cluster already exists. Deleting..."
    kwokctl delete cluster --name "kwok"
fi

kwokctl create cluster --kube-scheduler-config=manifests/scheduler-config.yaml
kubectl config use-context kwok-kwok

helm install kwok-nodes-provisioner charts/kwok-nodes-provisioner/

kubectl apply -f manifests/fake-pods.yaml

# //FIXME right now the following pods are not scheduled because disk labels are missing on the nodes.
# kubectl apply -f manifests/databases.yaml
# kubectl apply -f manifests/fake-backups.yaml
# kubectl apply -f manifests/web-servers.yaml
