# Lab07 - Kind + Google Online Boutique (microservices-demo)

Real multi-service workload lab for PODTetris.
Reuses the Kind cluster shape, cert-manager issuer, and Helm install flow from [lab6_kind](../lab6_kind/).

Workload: [GoogleCloudPlatform/microservices-demo](https://github.com/GoogleCloudPlatform/microservices-demo)
(Online Boutique), pinned release manifests.

## Prerequisites
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/)
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/)

## Running
```
chmod +x setup-cluster.sh
./setup-cluster.sh
```

The setup script:
1. Creates a Kind cluster (same node layout as lab6)
2. Installs cert-manager and the self-signed `ClusterIssuer`
3. Deploys Online Boutique release manifests (default namespace)
4. Drops `frontend-external` (LoadBalancer is unused on Kind)
5. Applies PDBs on key services
6. Scales selected services (default `SCALE_REPLICAS=3`) for packing pressure

Optional env overrides:
```
BOUTIQUE_VERSION=v0.10.7 SCALE_REPLICAS=2 ./setup-cluster.sh
```

Browse the shop:
```
kubectl port-forward svc/frontend 8080:80
# open http://localhost:8080
```

## Install PODTetris
```
# from repo root
make deploy-local-boutique
```

Or from this directory:
```
helm upgrade --install podtetris ../../charts/podtetris \
  -n podtetris --create-namespace \
  -f values-kind.yaml \
  -f values-boutique-rules.yaml
```

## Run an experiment
```
# from repo root (cluster already up, chart already installed)
./scripts/run-experiment.sh --skip-deploy
```
