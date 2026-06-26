package main

import (
	"fmt"
	"log"
	"sort"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/scheduling"
	kubeframework "k8s.io/kube-scheduler/framework"
	//caframework "k8s.io/autoscaler/cluster-autoscaler/simulator/framework"
)

func selectCandidateNodes(nodeInfos []kubeframework.NodeInfo) []kubeframework.NodeInfo {
	sort.Slice(nodeInfos, func(i, j int) bool {
		return nodeInfos[i].GetRequested().GetMilliCPU() < nodeInfos[j].GetRequested().GetMilliCPU()
	})

	if len(nodeInfos) < CANDIDATE_NODES_NUMBER {
		return nodeInfos
	}
	return nodeInfos[:CANDIDATE_NODES_NUMBER]
}

func virtuallyEvictPods(snapshot clustersnapshot.ClusterSnapshot, candidateNodes []kubeframework.NodeInfo) []*apiv1.Pod {
	var evictedPods []*apiv1.Pod

	for _, ni := range candidateNodes {
		nodeName := ni.Node().Name
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

			if err := snapshot.UnschedulePod(pod.Namespace, pod.Name, nodeName); err != nil {
				fmt.Printf("Warning: Failed to virtually evict pod %s/%s: %v\n", pod.Namespace, pod.Name, err)
				continue
			}

			fmt.Println("  - Evicted Pod:", pod.Name)
			evictedPodsNum++
			evictedPods = append(evictedPods, pod)
		}
		fmt.Printf("Evicted %d pods.\n", evictedPodsNum)
	}
	return evictedPods
}

func generatePermutations(evictedPods []*apiv1.Pod) [][]*apiv1.Pod {
	// Permutation 1: CPU requests decreasing
	podsByCPU := make([]*apiv1.Pod, len(evictedPods))
	copy(podsByCPU, evictedPods)
	sort.Slice(podsByCPU, func(i, j int) bool {
		return getPodCPURequests(podsByCPU[i]) > getPodCPURequests(podsByCPU[j])
	})

	// Permutation 2: Memory requests decreasing
	podsByMemory := make([]*apiv1.Pod, len(evictedPods))
	copy(podsByMemory, evictedPods)
	sort.Slice(podsByMemory, func(i, j int) bool {
		return getPodMemoryRequests(podsByMemory[i]) > getPodMemoryRequests(podsByMemory[j])
	})

	return [][]*apiv1.Pod{podsByCPU, podsByMemory}
}

func runSchedulingSimulation(snapshot clustersnapshot.ClusterSnapshot, permutations [][]*apiv1.Pod) {
	simulator := scheduling.NewHintingSimulator()
	fmt.Println("\n\n ### Pods scheduling simulation ###")

	for idx, orderedPods := range permutations {
		fmt.Printf("\nTesting permutation #%d...\n", idx)
		snapshot.Fork()

		statuses, _, err := simulator.TrySchedulePods(snapshot, orderedPods, scheduling.ScheduleAnywhere, true)
		if err != nil {
			log.Fatalf("Error during scheduling simulation of permutation #%d: %v", idx, err)
		}

		for _, status := range statuses {
			if status.NodeName == "" {
				log.Fatalf("Error during simulation: pod could not be scheduled in permutation #%d!", idx)
			}
		}

		fmt.Printf("Success! All pods scheduled successfully for permutation #%d\n", idx)
		snapshot.Revert()
	}
}
