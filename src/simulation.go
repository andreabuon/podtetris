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

type SchedulingResult struct {
	permutation   []*apiv1.Pod
	emptyNodesNum int
	cost          int
	score         int
}

func AllContainFixedPods(nodeInfos []kubeframework.NodeInfo) bool {
	if len(nodeInfos) == 0 {
		return false
	}

	for _, nodeInfo := range nodeInfos {
		foundFixedInCurrentNode := false
		for _, podInfo := range nodeInfo.GetPods() {
			pod := podInfo.GetPod()
			if pod == nil {
				continue
			}

			if annotationValue, ok := pod.Annotations[AnnotationFixed]; ok && annotationValue == "true" {
				foundFixedInCurrentNode = true
				break
			}
		}
		if !foundFixedInCurrentNode {
			return false
		}
	}
	return true
}

func selectCandidateNodes(nodeInfos []kubeframework.NodeInfo) []kubeframework.NodeInfo {
	sort.Slice(nodeInfos, func(i, j int) bool {
		return nodeInfos[i].GetRequested().GetMilliCPU() < nodeInfos[j].GetRequested().GetMilliCPU()
	})

	candidateNodes := nodeInfos[:CANDIDATE_NODES_NUMBER]

	if !AllContainFixedPods(candidateNodes) {
		return candidateNodes
	} else {
		//TODO select other nodes.
		return nil
	}
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

func runSchedulingSimulation(snapshot clustersnapshot.ClusterSnapshot, permutations [][]*apiv1.Pod) []SchedulingResult {
	fmt.Println("\n\n ### Pods scheduling simulation ###")

	simulator := scheduling.NewHintingSimulator()
	var schedulingResults []SchedulingResult

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

		fmt.Printf("Success! All pods have been scheduled successfully for permutation #%d\n", idx)

		emptyNodesNum := 0
		cost := 100 //TODO calculate this
		result := SchedulingResult{
			permutation:   orderedPods,
			emptyNodesNum: emptyNodesNum,
			cost:          cost,
			score:         (EMPTY_NODES_SCORE_WEIGHT * emptyNodesNum) - (COST_SCORE_WEIGHT*cost)/EMPTY_NODES_SCORE_WEIGHT + COST_SCORE_WEIGHT,
		}
		schedulingResults = append(schedulingResults, result)
		snapshot.Revert()
	}
	return schedulingResults
}
