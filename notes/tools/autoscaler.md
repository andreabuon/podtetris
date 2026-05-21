# Autoscaler notes 
Notes taken from the [official FAQ](https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md).

> **When does Cluster Autoscaler change the size of a cluster?**
> Cluster Autoscaler **increases** the size of the cluster when:
> - there are pods that failed to schedule on any of the current nodes due to insufficient resources.
> - adding a node similar to the nodes currently present in the cluster would help.
> 
> Cluster Autoscaler **decreases** the size of the cluster when some nodes are consistently unneeded for a significant amount of time.
> A node is unneeded when it has low utilization and all of its important pods can be moved elsewhere.


> **What types of pods can prevent CA from removing a node?**
>
> [read here](https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#what-types-of-pods-can-prevent-ca-from-removing-a-node)

> **Should I use a CPU-usage-based node autoscaler with Kubernetes?**
> No.

> **How is Cluster Autoscaler different from CPU-usage-based node autoscalers?**
> Cluster Autoscaler makes sure that all pods in the cluster have a place to run, no matter if there is any CPU load or not. Moreover, it tries to ensure that there are no unneeded nodes in the cluster.
> CPU-usage-based (or any metric-based) cluster/node group autoscalers don't care about pods when scaling up and down. As a result, they may add a node that will not have any pods, or remove a node that has some system-critical pods on it, like kube-dns. Usage of these autoscalers with Kubernetes is discouraged.

---

**How does scale-down work?**

Every 10 seconds (configurable by --scan-interval flag), if no scale-up is needed, Cluster Autoscaler checks which nodes are unneeded. A node is considered for removal when all below conditions hold:

- The sum of cpu requests and sum of memory requests of all pods running on this node (DaemonSet pods and Mirror pods are included by default but this is configurable with --ignore-daemonsets-utilization and --ignore-mirror-pods-utilization flags) are smaller than 50% of the node's allocatable. (Before 1.1.0, node capacity was used instead of allocatable.) Utilization threshold can be configured using `--scale-down-utilization-threshold` flag.
    All pods running on the node (except these that run on all nodes by default, like manifest-run pods or pods created by daemonsets) can be moved to other nodes. See What types of pods can prevent CA from removing a node? section for more details on what pods don't fulfill this condition, even if there is space for them elsewhere. While checking this condition, the new locations of all movable pods are memorized. With that, Cluster Autoscaler knows where each pod can be moved, and which nodes depend on which other nodes in terms of pod migration. Of course, it may happen that eventually the scheduler will place the pods somewhere else.

- It doesn't have scale-down disabled annotation (see How can I prevent Cluster Autoscaler from scaling down a particular node?)

If a node is unneeded for more than 10 minutes, it will be terminated. (This time can be configured by flags - please see I have a couple of nodes with low utilization, but they are not scaled down. Why? section for a more detailed explanation.) Cluster Autoscaler terminates one non-empty node at a time to reduce the risk of creating new unschedulable pods. The next node may possibly be terminated just after the first one, if it was also unneeded for more than 10 min and didn't rely on the same nodes in simulation (see below example scenario), but not together. Empty nodes, on the other hand, can be terminated in bulk, up to 10 nodes at a time (configurable by --max-empty-bulk-delete flag.)

What happens when a non-empty node is terminated? As mentioned above, all pods should be migrated elsewhere. Cluster Autoscaler does this by evicting them and tainting the node, so they aren't scheduled there again.

DaemonSet pods may also be evicted. This can be configured separately for empty (i.e. containing only DaemonSet pods) and non-empty nodes with --daemonset-eviction-for-empty-nodes and --daemonset-eviction-for-occupied-nodes flags, respectively. Note that the default behavior is different on each flag: by default DaemonSet pods eviction will happen only on occupied nodes. Individual DaemonSet pods can also explicitly choose to be evicted (or not). See How can I enable/disable eviction for a specific DaemonSet for more details.

Example scenario:

Nodes A, B, C, X, Y. A, B, C are below utilization threshold. In simulation, pods from A fit on X, pods from B fit on X, and pods from C fit on Y.

Node A was terminated. OK, but what about B and C, which were also eligible for deletion? Well, it depends.

Pods from B may no longer fit on X after pods from A were moved there. Cluster Autoscaler has to find place for them somewhere else, and it is not sure that if A had been terminated much earlier than B, there would always have been a place for them. So the condition of having been unneeded for 10 min may not be true for B anymore.

But for node C, it's still true as long as nothing happened to Y. So C can be terminated immediately after A, but B may not.

Cluster Autoscaler does all of this accounting based on the simulations and memorized new pod location. They may not always be precise (pods can be scheduled elsewhere in the end), but it seems to be a good heuristic so far.