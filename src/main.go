package main

import (
	"context"
	"fmt"
	"log"
	"sort"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/predicate"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/store"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/framework"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/scheduling"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

func main() {
	klog.InitFlags(nil)
	ctx := context.Background()

	kubeconfig := loadKubeConfig()
	schedulerConfig := loadSchedulerConfig()

	clientset, err := kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating live Kubernetes clientset: %v", err)
	}

	informerFactory := initInformerFactory(ctx, clientset)

	fwHandle, err := framework.NewHandle(informerFactory, schedulerConfig, false, false)
	if err != nil {
		log.Fatalf("Error creating framework handle: %v", err)
	}

	// CREATE THE CLUSTER STATE SNAPSHOT

	fmt.Println("Reading live state from active Kubernetes cluster...")
	nodesPointers, podsPointers := fetchClusterState(ctx, clientset)
	fmt.Printf("Cluster discovery completed. Found %d Nodes and %d Pods.\n", len(nodesPointers), len(podsPointers))

	snapshotStore := store.NewDeltaSnapshotStore(PARALLELISM)
	snapshot := predicate.NewPredicateSnapshot(snapshotStore, fwHandle, false, PARALLELISM, false)
	err = snapshot.SetClusterState(nodesPointers, podsPointers, nil, nil)
	if err != nil {
		log.Fatalf("Critical sandbox simulation failure during instantiation: %v", err)
	}

	nodeInfos, err := snapshot.NodeInfos().List()
	if err != nil {
		log.Fatalf("Error listing node infos: %v", err)
	}

	fmt.Printf("\nThe cluster contains %d nodes.\n", len(nodeInfos))

	// Selecting candidate nodes for rescheduling.

	sort.Slice(nodeInfos, func(i, j int) bool {
		return nodeInfos[i].GetRequested().GetMilliCPU() < nodeInfos[j].GetRequested().GetMilliCPU()
	})

	candidateNodes := nodeInfos[:CANDIDATE_NODES_NUMBER]

	// Evict pods from candidate nodes
	fmt.Printf("\nSelected %d Least-Allocated Nodes for pods consolidation:\n", CANDIDATE_NODES_NUMBER)
	for _, ni := range candidateNodes {
		fmt.Printf(" -> Node: %s (Current Pods: %d)\n", ni.Node().Name, len(ni.GetPods()))
	}

	var evictedPods []*apiv1.Pod
	for _, ni := range candidateNodes {
		nodeName := ni.Node().Name
		// Copy the pod references out before we start deleting them from the underlying map
		var podsOnNode []*apiv1.Pod
		for _, podInfo := range ni.GetPods() {
			podsOnNode = append(podsOnNode, podInfo.GetPod())
		}

		fmt.Printf("\nVirtually evicting %d pods from node %s...\n", len(podsOnNode), nodeName)
		evictedPodsNum := 0
		for _, pod := range podsOnNode {
			if ok, reason := isEvictable(pod); !ok {
				fmt.Printf("  -> Skipping pod %s/%s: %s\n", pod.Namespace, pod.Name, reason)
				continue
			}

			err := snapshot.UnschedulePod(pod.Namespace, pod.Name, ni.Node().Name)
			if err != nil {
				fmt.Printf("Warning: Failed to virtually evict pod %s/%s: %v\n", pod.Namespace, pod.Name, err)
				continue
			} else {
				fmt.Println("  - Evicted Pod:", pod.Name)
				evictedPodsNum++
			}
			evictedPods = append(evictedPods, pod)
		}
		fmt.Printf("Evicted %d pods.", evictedPodsNum)
	}

	// PERMUTATIONS GENERATION

	permutations := [][]*apiv1.Pod{}

	// Permutation 1: by CPU requests, decreasing
	podsByCPU := make([]*apiv1.Pod, len(evictedPods))
	copy(podsByCPU, evictedPods)
	sort.Slice(podsByCPU, func(i, j int) bool {
		return getPodCPURequests(podsByCPU[i]) > getPodCPURequests(podsByCPU[j])
	})

	// Permutation 2: by memory requests, decreasing
	podsByMemory := make([]*apiv1.Pod, len(evictedPods))
	copy(podsByMemory, evictedPods)
	sort.Slice(podsByMemory, func(i, j int) bool {
		return getPodMemoryRequests(podsByMemory[i]) > getPodMemoryRequests(podsByMemory[j])
	})

	permutations = append(permutations, podsByCPU, podsByMemory)

	// SIMULATION

	simulator := scheduling.NewHintingSimulator()

	fmt.Println("\n\n ### Pods schedulation simulation ### ")

	for permutationIndex, orderedPods := range permutations {
		fmt.Printf("\nTesting the permutation #%d...\n", permutationIndex)
		fmt.Println("Forking the snapshot... ")
		snapshot.Fork()

		statuses, _, err := simulator.TrySchedulePods(snapshot, orderedPods, scheduling.ScheduleAnywhere, true)
		if err != nil {
			log.Fatalf("Error during the scheduling simulation of the permutation #%d.", permutationIndex)
		}
		for _, status := range statuses {
			if status.NodeName == "" {
				log.Fatalf("Error during the scheduling simulation of the permutation #%d: a pod could not be scheduled!", permutationIndex)
			}
		}
		fmt.Printf("Success! The simulator scheduled all the pods of the permutation: %d\n", permutationIndex)

		fmt.Println("Reverting Snapshot to Baseline... ")
		snapshot.Revert()
		fmt.Println("Test completed. Proceding...")
	}

}
