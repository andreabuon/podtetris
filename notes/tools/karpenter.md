# Karpenter

## Karpenter consolidation

[from the official docs page](https://karpenter.sh/docs/concepts/)

**Consolidation**: Karpenter works to actively reduce cluster cost by identifying when:

- Nodes can be removed because the node is empty
- Nodes can be removed as their workloads will run on other nodes in the cluster.
- Nodes can be replaced with cheaper variants due to a change in the workloads.

---

[Consolidation](https://karpenter.sh/docs/concepts/disruption/#consolidation)

Consolidation is configured by `consolidationPolicy` and `consolidateAfter`.

`consolidationPolicy` determines the pre-conditions for nodes to be considered consolidatable, and are `WhenEmpty` or `WhenEmptyOrUnderutilized`.

If a node has no running non-daemon pods, it is considered empty. 

`consolidateAfter` can be set to indicate how long Karpenter should wait after a pod schedules or is removed from the node before considering the node consolidatable.

With `WhenEmptyOrUnderutilized`, Karpenter will consider a node consolidatable when its `consolidateAfter` has been reached, empty or not.


**Karpenter has two mechanisms for cluster consolidation**:
- **Deletion**
    A node is eligible for deletion if all of its pods can run on free capacity of other nodes in the cluster.
- **Replace**
    A node can be replaced if all of its pods can run on a combination of free capacity of other nodes in the cluster and a single lower price replacement node.

**Consolidation has three mechanisms that are performed in order to attempt to identify a consolidation action**:
- **Empty Node Consolidation**
    Delete any entirely empty nodes in parallel
- **Multi Node Consolidation**
    Try to delete two or more nodes in parallel, possibly launching a single replacement whose price is lower than that of all nodes being removed
- **Single Node Consolidation**
    Try to delete any single node, possibly launching a single replacement whose price is lower than that of the node being removed.

**It’s impractical to examine all possible consolidation options for multi-node consolidation, so Karpenter uses a heuristic to identify a likely set of nodes that can be consolidated. For single-node consolidation we consider each node in the cluster individually.**

When there are multiple nodes that could be potentially deleted or replaced, Karpenter chooses to consolidate the node that overall disrupts your workloads the least by preferring to terminate:
- Nodes running fewer pods
- Nodes that will expire soon
- Nodes with lower priority pods

>![Warning]
> Using preferred anti-affinity and topology spreads can reduce the effectiveness of consolidation!

---
### NodePool Disruption Budgets

You can rate limit Karpenter’s disruption through the NodePool’s `spec.disruption.budgets`.
If undefined, Karpenter will default to one budget with nodes: 10%.
Budgets will consider nodes that are actively being deleted for any reason, and will only block Karpenter from disrupting nodes voluntarily through drift, emptiness, and consolidation. Note that NodePool Disruption Budgets do not prevent Karpenter from terminating expired nodes.
Reasons

Karpenter allows specifying if a budget applies to any of `Drifted`, `Underutilized`, or `Empty`.
When a budget has no reasons, it’s assumed that it applies to all reasons.
When calculating allowed disruptions for a given reason, Karpenter will take the minimum of the budgets that have listed the reason or have left reasons undefined.

### Nodes

When calculating if a budget will block nodes from disruption, Karpenter lists the total number of nodes owned by a `NodePool`, subtracting out the nodes owned by that `NodePool` that are currently being deleted and nodes that are `NotReady`.
If the number of nodes being deleted by Karpenter or any other processes is greater than the number of allowed disruptions, disruption for this node will not proceed.