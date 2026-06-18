package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog/v2"

	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/predicate"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/store"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/framework"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/scheduling"
	"k8s.io/client-go/informers"
)

const PARALLELISM = 1

func main() {
	klog.InitFlags(nil)
	defer klog.Flush()

	home := homedir.HomeDir()
	if home == "" {
		log.Fatalf("Critical: Could not locate home directory to read kubeconfig.")
	}
	kubeconfig := filepath.Join(home, ".kube", "config")

	// Build the configuration client context
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Fatalf("Error loading local kubeconfig: %v", err)
	}

	// Create the standard client-go clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating live Kubernetes clientset: %v", err)
	}

	ctx := context.Background()
	fmt.Println("Reading live state from active Kubernetes cluster...")

	// Query active Nodes
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("API Error fetching active cluster nodes: %v", err)
	}

	// Query active Pods across ALL namespaces
	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("API Error fetching active cluster pods: %v", err)
	}

	fmt.Printf("Cluster discovery completed. Found %d Nodes and %d Pods.\n", len(nodes.Items), len(pods.Items))

	// Convert concrete API slices to the pointer slices (*apiv1.Node and *apiv1.Pod) that the Autoscaler's SetClusterState signature strictly expects.
	var nodePointers []*apiv1.Node
	for i := range nodes.Items {
		nodePointers = append(nodePointers, &nodes.Items[i])
	}

	var podPointers []*apiv1.Pod
	for i := range pods.Items {
		podPointers = append(podPointers, &pods.Items[i])
	}

	// Build an informer factory and a scheduler framework handle.
	informerFactory := informers.NewSharedInformerFactory(clientset, 0)
	fwHandle, err := framework.NewHandle(informerFactory, nil /* SchedulerConfig */, false /* DynamicResourceAllocationEnabled */, false)
	if err != nil {
		log.Fatalf("Error creating framework handle: %v", err)
	}

	stopCh := make(chan struct{})
	informerFactory.Start(stopCh)
	informerFactory.WaitForCacheSync(stopCh)

	snapshotStore := store.NewDeltaSnapshotStore(PARALLELISM)
	snapshot := predicate.NewPredicateSnapshot(snapshotStore, fwHandle, false, PARALLELISM, false)
	err = snapshot.SetClusterState(nodePointers, podPointers, nil, nil)
	if err != nil {
		log.Fatalf("Critical sandbox simulation failure during instantiation: %v", err)
	}

	nodeInfos, err := snapshot.NodeInfos().List()
	if err != nil {
		log.Fatalf("Error listing node infos: %v", err)
	}

	fmt.Printf("The cluster contains the following nodes:\n")
	for _, nodeInfo := range nodeInfos {
		fmt.Printf("- %s\n", nodeInfo.Node().Name)
	}
	fmt.Printf("\n--------------\n")

	fmt.Printf("The pods assigned to each node are:\n")

	for _, nodeInfo := range nodeInfos {
		node := nodeInfo.Node()
		nodePodsInfos := nodeInfo.GetPods()

		fmt.Printf("\n[Node] %s\n", node.Name)
		for _, podInfo := range nodePodsInfos {
			pod := podInfo.GetPod()
			fmt.Printf("    - [Pod] %s/%s\n", pod.Namespace, pod.Name)
		}
	}

	simulator := scheduling.NewHintingSimulator()

	// TEST: FORK THE SNAPSHOT, ADD A FAKE POD FORCEFULLY, CHECK AND REVERT
	fmt.Println("\n--- TEST #1---")

	snapshot.Fork()
	fmt.Println("\nSnapshot Forked (Checkpoint Created)")

	// Target the first node from your cluster for the injection simulation
	if len(nodeInfos) == 0 {
		log.Fatalf("No nodes available in snapshot to simulate pod injection.")
	}
	targetNodeName := nodeInfos[0].Node().Name

	fakePod := &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simulated-burst-pod-xyz",
			Namespace: "default",
			UID:       "simulated-uid-12345",
		},
		Spec: apiv1.PodSpec{
			Containers: []apiv1.Container{
				{
					Name:  "nginx",
					Image: "nginx:latest",
					Resources: apiv1.ResourceRequirements{
						Requests: apiv1.ResourceList{
							apiv1.ResourceCPU:    resource.MustParse("500m"),
							apiv1.ResourceMemory: resource.MustParse("512Mi"),
						},
					},
				},
			},
		},
	}

	// Force inject the fake pod into the snapshot
	fmt.Printf("Simulating placement of fake pod '%s' onto Node '%s'...\n", fakePod.Name, targetNodeName)
	err = snapshot.ForceAddPod(fakePod, targetNodeName)
	if err != nil {
		log.Fatalf("Failed to force add pod to snapshot: %v", err)
	}

	// Verify the pod exists inside the snapshot post-injection
	updatedNodeInfo, err := snapshot.NodeInfos().Get(targetNodeName)
	if err == nil {
		fmt.Printf("Verified: Node '%s' now has %d pod(s) running in simulation.\n",
			targetNodeName, len(updatedNodeInfo.GetPods()))
	}

	fmt.Println("\nReverting Snapshot to Baseline")
	snapshot.Revert()

	// Verify the pod was cleanly removed by the revert operation
	revertedNodeInfo, err := snapshot.NodeInfos().Get(targetNodeName)
	if err == nil {
		fmt.Printf("Verified after Revert: Node '%s' is back to %d pod(s).\n",
			targetNodeName, len(revertedNodeInfo.GetPods()))
	}

	// TEST: FORK THE SNAPSHOT, ADD A FAKE POD, SCHEDULE IT, CHECK AND REVERT
	fmt.Println("\n--- TEST #2---")

	snapshot.Fork()
	fmt.Println("\nSnapshot Forked (Checkpoint Created)")

	fakePod2 := &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simulated-burst-pod-xyz",
			Namespace: "default",
			UID:       "simulated-uid-12345",
		},
		Spec: apiv1.PodSpec{
			Containers: []apiv1.Container{
				{
					Name:  "nginx",
					Image: "nginx:latest",
					Resources: apiv1.ResourceRequirements{
						Requests: apiv1.ResourceList{
							apiv1.ResourceCPU:    resource.MustParse("500m"),
							apiv1.ResourceMemory: resource.MustParse("512Mi"),
						},
					},
				},
			},
		},
	}

	statuses, _, err := simulator.TrySchedulePods(snapshot, []*apiv1.Pod{fakePod2}, scheduling.ScheduleAnywhere, false)

	if len(statuses) > 0 && statuses[0].NodeName != "" {
		fmt.Printf("Success! The simulator scheduled the pod onto Node: %s\n", statuses[0].NodeName)
	} else {
		fmt.Println("Simulation Complete: Pod is unschedulable on current cluster capacity.")
	}

	fmt.Println("\nReverting Snapshot to Baseline...")
	snapshot.Revert()

	// TEST: FORK THE SNAPSHOT, ADD A GIANT FAKE POD, SCHEDULE IT, CHECK AND REVERT
	fmt.Println("\n--- TEST #3---")

	snapshot.Fork()
	fmt.Println("\nSnapshot Forked (Checkpoint Created)")

	fakePod3 := &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simulated-burst-pod-xyz",
			Namespace: "default",
			UID:       "simulated-uid-12345",
		},
		Spec: apiv1.PodSpec{
			Containers: []apiv1.Container{
				{
					Name:  "nginx",
					Image: "nginx:latest",
					Resources: apiv1.ResourceRequirements{
						Requests: apiv1.ResourceList{
							apiv1.ResourceCPU:    resource.MustParse("500000000m"),
							apiv1.ResourceMemory: resource.MustParse("512000000Mi"),
						},
					},
				},
			},
		},
	}

	statuses, _, err = simulator.TrySchedulePods(snapshot, []*apiv1.Pod{fakePod3}, scheduling.ScheduleAnywhere, false)

	if len(statuses) > 0 && statuses[0].NodeName != "" {
		fmt.Printf("Success! The simulator scheduled the pod onto Node: %s\n", statuses[0].NodeName)
	} else {
		fmt.Println("Simulation Complete: Pod is unschedulable on current cluster capacity.")
	}

	fmt.Println("\nReverting Snapshot to Baseline...")
	snapshot.Revert()
}
