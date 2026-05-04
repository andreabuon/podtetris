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

> **How does Cluster Autoscaler remove nodes?**
> Cluster Autoscaler terminates the underlying instance in a cloud-provider-dependent manner.
> It does not delete the Node object from Kubernetes. Cleaning up Node objects corresponding to terminated instances is the responsibility of the cloud node controller, which can run as part of kube-controller-manager or cloud-controller-manager.

> **I'm running cluster with nodes in multiple zones for HA purposes. Is that supported by Cluster Autoscaler?**
> CA 0.6 introduced --balance-similar-node-groups flag to support this use case. If you set the flag to true, CA will automatically identify node groups with the same instance type and the same set of labels (except for automatically added zone label) and try to keep the sizes of those node groups balanced.
> This does not guarantee similar node groups will have exactly the same sizes:
> - Currently the balancing is only done at scale-up. Cluster Autoscaler will still scale down underutilized nodes regardless of the relative sizes of underlying node groups. We plan to take balancing into account in scale-down in the future.
> - Cluster Autoscaler will only add as many nodes as required to run all existing pods. If the number of nodes is not divisible by the number of balanced node groups, some groups will get 1 more node than others.
> - Cluster Autoscaler will only balance between node groups that can support the same set of pending pods. If you run pods that can only go to a single node group (for example due to nodeSelector on zone label) CA will only add nodes to this particular node group.
> You can opt-out a node group from being automatically balanced with other node groups using the same instance type by giving it any custom label.

> **How can I monitor Cluster Autoscaler?**
> Cluster Autoscaler provides metrics and livenessProbe endpoints. By default they're available on port 8085 (configurable with --address flag), respectively under /metrics and /health-check.
> Metrics are provided in Prometheus format and their detailed description is available here.