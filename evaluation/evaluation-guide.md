# PODTetris: Thesis Evaluation & Experimental Design Guide

Evaluating **PODTetris** for a thesis requires demonstrating two core capabilities simultaneously:
1. **Algorithmic Quality:** Your offline bin-packing model consolidates nodes more effectively than existing heuristics while respecting fixed-pod rules and custom pod migration costs.
2. **Systems Viability:** Your controller, eviction pipeline, and mutating admission webhook operate safely and quickly inside a real Kubernetes control plane without breaking workloads or creating race conditions with `kube-scheduler` and the Cluster Autoscaler (CA).

---

## 1. Evaluation Metrics Framework

Organize your thesis results into three distinct metric categories so reviewers can clearly evaluate the trade-off between **cluster resource savings**, **workload disruption**, and **control-plane overhead**.

### A. Consolidation & Resource Efficiency (The "Gain")

* **Active Node Count & Node-Hours Saved:** Track the number of active nodes over time $N(t)$ and total cumulative node-hours $\int_{0}^{T} N(t)\,dt$. Compare the final node count after a PODTetris pass against the theoretical lower bound:
  $$N_{\min} = \max\left(\left\lceil \frac{\sum_{p \in P} R_{\text{CPU}}(p)}{C_{\text{CPU}}} \right\rceil, \left\lceil \frac{\sum_{p \in P} R_{\text{Mem}}(p)}{C_{\text{Mem}}} \right\rceil\right)$$
* **Cluster Resource Utilization (Requested vs. Allocatable):** Measure the shift in average CPU and memory request utilization across remaining active nodes before and after consolidation.
* **Stranded Resource / Multi-Dimensional Fragmentation Ratio:** A node at $90\%$ CPU and $25\%$ Memory has $65\%$ stranded memory that cannot be easily claimed by new pods. Quantify node resource imbalance across active nodes $N_{\text{active}}$:
  $$I_{\text{cluster}} = \frac{1}{|N_{\text{active}}|} \sum_{n \in N_{\text{active}}} \left| U_{\text{CPU}}(n) - U_{\text{Mem}}(n) \right|$$
* **Stuck-Node Recovery Rate:** Count how many underutilized nodes (e.g., between $30\%$ and $65\%$ utilization) the standard Cluster Autoscaler failed to scale down on its own, which PODTetris successfully drained and handed over to CA.

### B. Disruption & Cost-Model Effectiveness (The "Pain")

* **Total Migration Cost vs. Raw Eviction Count:** If each pod $p$ has a custom migration cost $c_p$, compare the total weighted eviction cost $\sum_{p \in P_{\text{evicted}}} c_p$ against a cost-blind baseline. This proves your rule engine works—for example, showing PODTetris chooses to evict four cheap stateless pods ($4 \times 1 = 4$) rather than one expensive warm-cache pod ($1 \times 10 = 10$).
* **Cost Efficiency per Drained Node:**
  $$\text{Efficiency} = \frac{\sum_{p \in P_{\text{evicted}}} c_p}{\Delta N_{\text{drained}}}$$
* **Constraint Violation Rate:** Confirm $0\%$ evictions of "fixed" pods and $0$ `PodDisruptionBudget` (PDB) violations during eviction waves.
* **End-to-End Pod Rescheduling Downtime:** Measure the lifecycle latency of a moved pod across four stages:
  $$\text{Eviction API Call} \rightarrow \text{ReplicaSet Creates Replacement} \rightarrow \text{Webhook Mutates Pod} \rightarrow \text{PodReady}$$

### C. Control-Plane & Solver Overhead (The "Scalability")

* **Bin-Packing Solver Execution Time:** Plot solver wall-clock time and memory usage as cluster scale grows (e.g., $50$, $250$, $500$, $1000$ pods) and as rule complexity increases.
* **Mutating Webhook Latency ($p_{50}$, $p_{95}$, $p_{99}$):** Since your mutating admission webhook intercepts pod creations, report its latency via Prometheus (`apiserver_admission_webhook_admission_duration_seconds_bucket`). Reviewers will want proof that PODTetris adds negligible overhead (e.g., $< 5\text{--}10\text{ ms}$) to normal cluster pod creations.
* **Plan Invalidation / Race Rate:** Measure how often a computed packing plan fails mid-execution because an external pod arrived and consumed capacity on a target node before the evicted pod was recreated.

---

## 2. Two-Tier Test Environment Architecture

Running $100+$ real cloud VMs to test cluster autoscaling is prohibitively expensive and slow. The gold standard for Kubernetes scheduling theses is a **two-tier evaluation setup** combining a high-scale control-plane simulator with a smaller real-node cluster.

| Tier | Tooling Stack | Cluster Scale | What It Evaluates in Your Thesis |
| :--- | :--- | :--- | :--- |
| **Tier 1: Large-Scale Control-Plane Simulation** | **KWOK** (`Kubernetes WithOut Kubelet`) + **Cluster Autoscaler (`kwok` provider)** + **SimKube** | $50\text{--}1000$ nodes, $500\text{--}10000$ pods (runs on 1 laptop/VM) | Solver scalability, bin-packing efficiency, node consolidation counts, trace replay, and interaction with the real Cluster Autoscaler loop. |
| **Tier 2: High-Fidelity Real Cluster** | **Kind** (multi-node) or **k3s / Cloud VMs** ($5\text{--}15$ nodes) + **Prometheus / Grafana** + **k6 / Locust** | $5\text{--}15$ nodes, $50\text{--}150$ real pods | Real container termination/startup latency, mutating webhook $p_{99}$ latency, PDB adherence, and HTTP request error rates during consolidation. |

### How to Set Up Each Tier

1. **Use KWOK + Cluster Autoscaler for Scale Testing:**
   * **KWOK** runs a real `kube-apiserver`, `kube-controller-manager`, and `kube-scheduler`, alongside your real PODTetris controller and mutating webhook, while simulating node heartbeats and pod lifecycles in memory.
   * The official Kubernetes `autoscaler` repository includes a native **KWOK CloudProvider** (`--cloud-provider=kwok`). This allows the real Cluster Autoscaler binary to watch your simulated nodes and scale them down once PODTetris empties them.
2. **Use SimKube for Deterministic Trace Replay:**
   * **SimKube** (built on KWOK) records and replays Kubernetes event traces deterministically. You can record one 2-hour workload trace and replay it identically under Vanilla CA, K8s Descheduler, and PODTetris to obtain an exact apples-to-apples comparison.
3. **Configure KWOK Stage Delays for Realism:**
   * By default, KWOK transitions pods to `Running` instantly. Configure KWOK `Stage` custom resources to inject realistic delays (e.g., $2\text{--}5\text{ seconds}$ for container image pull/startup and $5\text{--}15\text{ seconds}$ for `TerminationGracePeriodSeconds`) so you can observe realistic overlapping eviction waves.

---

## 3. High-Impact Baselines, Scenarios & Thesis Tips

### Baselines to Compare Against

To prove PODTetris is necessary, benchmark four configurations on the exact same initial cluster states:

1. **Vanilla Cluster Autoscaler Only:** By default, CA only scales down a node if its utilization is below $50\%$ (`--scale-down-utilization-threshold=0.5`) and all pods are movable. Show how two nodes sitting at $55\%$ utilization stay alive indefinitely under Vanilla CA.
2. **Kubernetes `Descheduler` (`LowNodeUtilization` / `HighNodeUtilization`) + CA:** This is your **primary architectural competitor**. In your thesis, highlight the fundamental limitation of the standard `Descheduler`: it evicts pods blindly without knowing where `kube-scheduler` will place them, often shuffling pods without actually emptying a node. PODTetris solves this via **offline global planning + mutating webhook target pinning**.
3. **PODTetris (Cost-Unaware / Uniform Cost):** Runs your bin-packing + webhook pipeline assuming all movable pods have equal cost ($c_p = 1$).
4. **PODTetris (Cost-Aware + Fixed Rules):** Your full system, demonstrating that you achieve nearly the same node reduction while substantially reducing total migration cost $\sum c_p$ by avoiding expensive pods.

### Workload Scenarios to Run

* **Scenario 1: The "Post-Peak Swiss Cheese" (Diurnal Scale-Down):** Fill a 30-node cluster to $85\%$ capacity, then randomly delete $50\%$ of the pods (simulating traffic dropping after peak hours). Every node is now approximately $40\text{--}55\%$ full with scattered fixed pods—the exact scenario where standard autoscalers stall.
* **Scenario 2: Complementary Multi-Resource "Tetris" Workload:** Mix **CPU-bound** pods (e.g., `2000m CPU, 512Mi RAM`) with **Memory-bound** pods (e.g., `250m CPU, 8Gi RAM`). Show how `kube-scheduler`'s default `LeastAllocated` scoring spreads them poorly, whereas PODTetris pairs complementary pods onto the same nodes to unlock extra scale-downs.
* **Scenario 3: Real-World Production Distribution (Alibaba Cluster Trace):** Sample pod CPU/Memory requests from the public **Alibaba Cluster Trace** (`cluster-trace-v2018` or `cluster-trace-gpu-v2023`) to prove your bin-packing algorithm works on skewed, non-uniform production pod sizes rather than just synthetic uniform sizes.

### Three Architectural Nuances to Discuss in Your Thesis

* **Matching Recreated Pods in the Mutating Webhook:** When your controller evicts `pod-abc-123`, the `ReplicaSet` creates a brand-new pod `pod-abc-999`. In your evaluation and design sections, explain how your mutating webhook maps the newly created pod back to the target node in the bin-packing plan (e.g., keying the plan's target-node queue by `ownerReferences` UID or template-hash label) and how it uses node affinity (`requiredDuringSchedulingIgnoredDuringExecution`) rather than directly bypassing the scheduler via `spec.nodeName` (since setting `spec.nodeName` directly skips scheduler predicate checks and can cause resource race conditions).
* **Eviction Pacing vs. Cluster Autoscaler Intervals:** Compare **All-at-Once Eviction** vs. **Batched/Rate-Limited Eviction**. Evicting 50 pods at once drains nodes fastest but spikes API server load and drops service capacity; batching evictions preserves availability at the cost of slower consolidation.
* **Solver Comparison (Heuristic vs. Exact):** Including a benchmark table comparing **First-Fit Decreasing (FFD)** against an **ILP / CP-SAT solver** (such as Google OR-Tools) inside PODTetris—showing solve time vs. nodes saved—adds strong quantitative rigor to your thesis evaluation chapter.

---

## 4. Quick Experimental Checklist for Your Thesis Chapter

| Experiment ID | Research Question Answered | Primary Metric(s) | Recommended Testbed |
| :--- | :--- | :--- | :--- |
| **EXP-1: Consolidation Efficacy** | How many more nodes does PODTetris drain compared to Vanilla CA and K8s Descheduler? | Active Node Count $N(t)$, Stranded Resource Index $I_{\text{cluster}}$ | Tier 1 (KWOK + CA, 50–100 nodes) |
| **EXP-2: Cost-Awareness Impact** | Does the custom cost model reduce expensive pod evictions without sacrificing node scale-down? | Total Migration Cost $\sum c_p$, Cost per Drained Node | Tier 1 (KWOK + CA, 50 nodes) |
| **EXP-3: Solver Scalability** | How fast does the offline bin-packing algorithm run as cluster size grows? | Solver execution time (ms/s) vs. Pod Count ($100 \rightarrow 5000$) | Standalone Solver Benchmark / Tier 1 |
| **EXP-4: Control-Plane & Workload Safety** | What is the real-world latency overhead of the webhook and disruption to live traffic during pod shuffling? | Webhook $p_{99}$ latency, Pod Rescheduling Downtime, HTTP 5xx rate | Tier 2 (Kind / k3s + Prometheus + k6) |