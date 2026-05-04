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
# kubectl apply -f manifests/multiple-deployments.yaml //FIXME right now pods are not scheduled because disk labels are missing