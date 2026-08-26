# Lab06 - kwok + kind (PODTetris cost / webhook lab)

## Prerequisites
This lab requires installing:
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/)
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/)

## Running
```
chmod +x setup-cluster.sh
./setup-cluster.sh
```

The setup script:
1. Creates a Kind cluster
2. Installs [cert-manager](https://cert-manager.io/) and a self-signed `ClusterIssuer` (`selfsigned-issuer`)
3. Applies test workloads

## Install PODTetris chart
Use `values-kind.yaml` so webhook certificates use the local issuer (not the company Step issuer):

```
helm upgrade --install podtetris ../../charts/podtetris \
  -n podtetris --create-namespace \
  -f values-kind.yaml
```
