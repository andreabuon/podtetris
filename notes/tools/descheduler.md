# Descheduler notes

[from the official README page](https://github.com/kubernetes-sigs/descheduler/blob/master/README.md)

The scheduler's decisions are influenced by its view of a Kubernetes cluster at that point of time when a new pod appears for scheduling.
As Kubernetes clusters are very dynamic and their state changes over time, there may be desire to move already running pods to some other nodes for various reasons:
- Some nodes are under or over utilized.
- The original scheduling decision does not hold true any more, as taints or labels are added to or removed from nodes, pod/node affinity requirements are not satisfied any more.
- Some nodes failed and their pods moved to other nodes.
- New nodes are added to clusters.

Consequently, there might be several pods scheduled on less desired nodes in a cluster.
Descheduler, based on its policy, finds pods that can be moved and evicts them. 
Please note, in current implementation, **descheduler does not schedule replacement of evicted pods but relies on the default scheduler for that**.

## HighNodeUtilization policy

[HighNodeUtilization policy](https://github.com/kubernetes-sigs/descheduler/blob/master/README.md#highnodeutilization)

`HighNodeUtilization` policy + node auto-scaling
    This strategy must be used with the scheduler scoring strategy `MostAllocated`

Balance low utilization nodes

[from HighNodeUtilization user guide](https://github.com/kubernetes-sigs/descheduler/blob/master/docs/user-guide.md#balance-low-utilization-nodes)
Using `HighNodeUtilization`, descheduler will rebalance the cluster based on memory by evicting pods from nodes with memory utilization lower than 20%.
This should be use `NodeResourcesFit` with the `MostAllocated` scoring strategy based on these doc.
The evicted pods will be compacted into minimal set of nodes.