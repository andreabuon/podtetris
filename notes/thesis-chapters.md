# PODTetris thesis

## Chapters

### 1. Introduction

- Context
  - PoliTO
  - Logistics Reply

- The industrial problem: resource fragmentation on Kubernetes
- Goals and non-goals
- Constraints of the setting (preview)
  - Amazon EKS: no control-plane / scheduler modifications
  - custom scheduler plugins would require a second scheduler instance
  - metrics-based schedulers should not be used (according to the documentation)
- Contributions
- Thesis outline

### 2. Background

- Containers and Docker (brief)

- Kubernetes

- Kubernetes objects
  - Pods
  - Deployments / ReplicaSets
  - StatefulSets
  - DaemonSets, Jobs
  - system pods / system namespaces

- Scheduling constraints
  - affinity / anti-affinity
  - nodeSelector
  - topology (zones / areas)
  - Pod Disruption Budgets

- Default scheduler
  - Filter + Scoring
  - LeastAllocated / MostAllocated
  - binding is final: no shuffle, pods cannot be moved from a node to another

- Bin packing as the conceptual model
  - NP-hard
  - First fit, Best fit, etc
  - how this maps to LeastAllocated / MostAllocated

- Managed Kubernetes (Amazon EKS)
  - you cannot add scheduler plugins on the default scheduler
  - a second scheduler is possible but not the product path

### 3. The problem

#### 3.1 Resource fragmentation

- Definition: spare CPU/memory exists in the cluster, but not on any single node in a shape that a pending pod can use
- How it appears in Kubernetes
  - stranded resources (e.g. free CPU on a node with no free memory, or the opposite)
  - unschedulable pods despite unused capacity
  - extra nodes added instead of reshaping the packing
- Why it accumulates
  - default scheduler binding is final
  - create / delete / scale of Deployments over time, with no repack
  - heterogeneous requests vs node sizes
  - constraints (affinity, topology, PDBs) shrink the feasible placements
- Cost: extra nodes, worse utilization, higher cloud bill
- What “solving it” would mean: fewer nodes for the same workload, without violating scheduling constraints or causing unbounded disruption

#### 3.2 Existing tools

Why they do not solve 3.1.

- Cluster Autoscaler
  - fixed utilization threshold
  - no pods shuffling
  - replacement pods: CA only checks whether they fit on other nodes, no destination decision (left to the scheduler)
  - reacts to pending pods by adding nodes (worsens fragmentation)
- Descheduler
  - pros
  - cons: no guarantee on where the pod lands
    - eviction hoping pods will not return to the same nodes → useless disruption
- AWS Karpenter
  - fixed threshold
  - consolidation by instance size
- Coexistence with Cluster Autoscaler
  - planning on Pending pods races with CA scale-up

#### 3.3 Requirements

- work with the default EKS scheduler
- fail-open
    if our components die, the cluster still schedules
- respect affinity, topology, PDBs
- bound disruption
- idempotent actuation
- physical node removal can be delegated to Cluster Autoscaler (cloud provider specific code)

### 4. Related work

Compare against chapter 3.

- OR-Tools (2 papers)
- Tesi Bologna (Kubernetes)
- Paper Canova
- Descheduler impact on resource fragmentation
- Fondazione Kessler
- Comparison table vs PODTetris

### 5. Design of PODTetris

Simulator / planner computes a target packing; actuator applies it; Cluster Autoscaler removes emptied nodes.

- High-level architecture
  - planner
  - CRDs as shared state (how components communicate)
  - controller (evictor)
  - mutating admission webhook
  - Cluster Autoscaler for physical node deletion

- Design alternatives we tried
  - scale up / scale down to avoid disruption
    - race conditions
    - unclear which pod to evict
  - cordon nodes
  - eviction + webhook (chosen)

- Planner
  - limit computation to a subset of the cluster to bound disruption, while minimizing nodes under configured constraints
  - node selection: non-deterministic + deterministic
  - when it runs: cron vs when a pod does not fit and stays Pending (conflict with Cluster Autoscaler)
  - Cluster Autoscaler snapshot tools
  - CA simulator is filter-only; we add real scheduler function calls
  - pod permutations (by CPU, by memory, random) and re-scheduling
  - use of Kubernetes informers
- Failure model
  - controller down → nothing happens (no partial damage)
  - webhook down → default scheduler can still place pods according to its default rules

### 6. Implementation

- Deploy via Helm chart

- Planner configuration (ConfigMap)
  - weights / scoring rules
  - pinned / fixed nodes
  - regex
  - ignore DaemonSets, Jobs, system pods, system namespaces

- CRDs + Kubebuilder controller
  - ConsolidationPlan
  - PodMove
  - idempotency
  - a new pod cancels the current plan
  - maxConcurrentReconciles = 1

- Mutating admission webhook
  - beat the default scheduler on node selection (`nodeName`)
  - sideEffects
  - pod must already be persisted
  - Deployments / ReplicaSets: only the target node matters
  - StatefulSets: match by stable name (need the original identity)
  - availability: multiple replicas
  - fail-open if the webhook is down

### 7. Evaluation

- Testbeds and what each is valid for
  - Kind
  - KWOK
  - EKS
- Metrics (nodes saved, total moves number, total moves cost, races with CA, plan abort)
- Results analysis
- Threats to validity

### 8. Conclusions and future work

- Recap of the problem, constraints, and contribution
- Limitations
- Possible improvements
  - persist simulated node state
    - if a new pod arrives, check whether it still fits the planned packing
    - if yes, keep the plan; otherwise abort
