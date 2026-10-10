# Lab08 - KWOK

Production-shaped packing lab for the planner. Workloads are pause pods on [KWOK](https://kwok.sigs.k8s.io/) nodes, so nothing pulls images or runs a container. Kind runs a tainted control-plane plus one real worker for cert-manager, KWOK, and PODTetris.

Online Boutique is a single namespace of ~11 services on a handful of Kind nodes. Requests are tiny next to a Kind worker, so the scheduler never has to leave a hole that the next pod cannot fill. This lab is the opposite shape: many namespaces, mixed request sizes, a memory pool, anti-affinity, PDBs, one fixed StatefulSet, and more nodes than the steady workload needs.

## Why the default scheduler is enough

`NodeResourcesFit` scores **LeastAllocated** by default (CPU and memory, weight 1). That prefers the node with the most free capacity as a fraction of allocatable, so with spare nodes every one of them gets pods. The planner scores **MostAllocated** and tries to undo that spread.

A custom MostAllocated kube-scheduler would pack the cluster up front and leave the planner with nothing to consolidate. Pinning LeastAllocated in a kube-scheduler config file is unnecessary: it is already the default, and a partial scheduler config is easy to get wrong on Kind (it replaces kube-scheduler’s kubeconfig path).

Spread alone is only half of a real cluster. After a traffic spike, replicas are deleted and the remaining pods stay where they were scheduled. Setup does that on purpose:

1. Apply the workload at `replicaMode=peak` and wait until the default scheduler places it.
2. Helm-upgrade to `replicaMode=steady`. Only `spec.replicas` changes, so surviving pods are not rescheduled.

Steady requests are about **46 CPU** on **24 × 3800m ≈ 91 CPU** allocatable. A perfect pack fits on about 12 nodes. Request sizes (1500m, 1700m, 2000m) do not tile a 3800m node, and a few workloads cannot share a node.

## Cluster shape

| Pool | Instance | Nodes | Allocatable |
| --- | --- | --- | --- |
| general | m6i.xlarge | 18 | 3800m / 14Gi |
| memory | r6i.xlarge | 6 | 3800m / 30Gi |

KWOK nodes are tainted `podtetris.io/workload=true:NoSchedule`. Benchmark pods tolerate that and require `type=kwok`, so they cannot land on the real worker. That worker is labeled `podtetris.io/system=true`, which the planner skips. Its allocatable is the whole Docker VM and would otherwise look like the emptiest consolidation candidate.

`search-indexer` and `search-query` are pinned to the memory pool. `payments-redis` is a fixed pod. `session` is a StatefulSet with required anti-affinity and move cost 100 (no volumes: KWOK has no CSI). `node-exporter` is a DaemonSet on the fake nodes; the planner treats DaemonSets as residual, same as kube-proxy.

## Prerequisites

- kubectl, Helm, kind
- KWOK is installed from its Helm repo by the setup script (no kwokctl binary)

## Running

```
chmod +x setup-cluster.sh
./setup-cluster.sh
```

## Install PODTetris

```
# from repo root
make deploy-local-kwok
```

Or from this directory:

```
helm upgrade --install podtetris ../../charts/podtetris \
  -n podtetris --create-namespace \
  -f values-kind.yaml \
  -f values-kwok-rules.yaml
```
