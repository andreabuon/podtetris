package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot"

	//"k8s.io/autoscaler/cluster-autoscaler/simulator/scheduling"
	kubeframework "k8s.io/kube-scheduler/framework"
	schedframework "k8s.io/kubernetes/pkg/scheduler/framework"
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

			if annotationValue, ok := pod.Annotations[Config.FixedPodAnnotation]; ok && annotationValue == "true" {
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

func selectCandidateNodes(nodeInfos []kubeframework.NodeInfo, nodesToGet int) ([]kubeframework.NodeInfo, error) {
	if nodeInfos == nil {
		return nil, errors.New("No available candidate nodes")
	}

	if AllContainFixedPods(nodeInfos) {
		return nil, errors.New("Every node in the cluster contains fixed pods")
	}

	if len(nodeInfos) <= nodesToGet {
		return nodeInfos, nil
	}

	// TODO add multiple ways to select the nodes
	/*
		sort.Slice(
			nodeInfos,
			func(i, j int) bool {
				return nodeInfos[i].GetRequested().GetMilliCPU() < nodeInfos[j].GetRequested().GetMilliCPU()
			})
	*/

	var candidateNodes []kubeframework.NodeInfo

	attemptNum := 0
	for {
		candidateNodes = nil
		attemptNum++

		randomIndices := rand.Perm(len(nodeInfos))
		for i := 0; i < nodesToGet; i++ {
			randomIndex := randomIndices[i]
			candidateNodes = append(candidateNodes, nodeInfos[randomIndex])
		}

		if !AllContainFixedPods(candidateNodes) {
			return candidateNodes, nil
		}

		if attemptNum >= Config.CandidateNodesSelectionMaxRetries {
			return nil, errors.New("Max candidate nodes selection retries reached")
		}
	}
}

func virtuallyEvictPods(snapshot clustersnapshot.ClusterSnapshot, candidateNodes []kubeframework.NodeInfo) []*apiv1.Pod {
	var evictedPods []*apiv1.Pod

	fmt.Printf("\nSimulating the pods eviction from the candidate nodes in the snapshot...\n")
	for nodeIndex, nodeInfo := range candidateNodes {
		nodeName := nodeInfo.Node().Name
		var podsOnNode []*apiv1.Pod
		for _, podInfo := range nodeInfo.GetPods() {
			podsOnNode = append(podsOnNode, podInfo.GetPod())
		}

		fmt.Printf("\n[#%d] Node: %s\n", nodeIndex, nodeName)
		evictedPodsNum := 0
		for _, pod := range podsOnNode {
			if ok, reason := isEvictable(pod); !ok {
				fmt.Printf("  > Skipping pod %s/%s: %s\n", pod.Namespace, pod.Name, reason)
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

func runSchedulingSimulation(realFramework schedframework.Framework, snapshot clustersnapshot.ClusterSnapshot, permutation []*apiv1.Pod, candidateNodesToDrain []kubeframework.NodeInfo, previousPodAllocations map[string]string, previousEmptyNodesNum int, ctx context.Context) (*SchedulingResult, error) {
	//simulator := scheduling.NewHintingSimulator()
	//simulator.DropOldHints()
	snapshot.Fork()

	permutationCost := 0

	/*
		statuses, _, err := simulator.TrySchedulePods(snapshot, permutation, scheduling.ScheduleAnywhere, true)
		if err != nil {
			fmt.Printf("Error during scheduling simulation: %v", err)
			snapshot.Revert()
			return nil, err
		}
	*/
	for _, pod := range permutation {
		state := schedframework.NewCycleState()
		//var newState kubeframework.CycleState = kubeframework

		// Step 1: Filter — which candidate nodes can take this pod?
		var feasible []kubeframework.NodeInfo
		for _, nodeInfo := range candidateNodesToDrain {
			_, preFilterStatus, _ := realFramework.RunPreFilterPlugins(ctx, state, pod)
			if !preFilterStatus.IsSuccess() {
				continue
			}

			filterStatus := realFramework.RunFilterPlugins(ctx, state, pod, nodeInfo)
			if filterStatus.IsSuccess() {
				feasible = append(feasible, nodeInfo)
			}
		}

		if len(feasible) == 0 {
			// no node works for this pod
			//FIXME this should return an error
			continue
		}

		// Step 2: Score — rank the feasible nodes
		preScoreStatus := realFramework.RunPreScorePlugins(ctx, state, pod, feasible)

		if !preScoreStatus.IsSuccess() {
			fmt.Printf("Error: PreScorePlugins failed")
			snapshot.Revert()
			return nil, errors.New("Error: PreScorePlugins failed")
		}

		scores, status := realFramework.RunScorePlugins(ctx, state, pod, feasible)
		if !status.IsSuccess() {
			fmt.Printf("Error: ScorePlugins failed")
			snapshot.Revert()
			return nil, errors.New("Error: ScorePlugins failed")
		}

		// Step 3: pick the winner — highest combined score
		bestNode, err := pickHighestScoreNode(feasible, scores)
		if err != nil {
			fmt.Printf("Error: pickHighestScoreNode failed")
			snapshot.Revert()
			return nil, errors.New("Error: pickHighestScoreNode failed")
		}

		// Step 4: apply it to the snapshot, same as before
		snapshot.ForceAddPod(pod, bestNode.Node().Name)

		fmt.Printf("Pod %s has been scheduled on node %s\n", pod.Name, bestNode.Node().Name)

		podKey := pod.Namespace + "/" + pod.Name
		if bestNode.Node().Name == previousPodAllocations[podKey] {
			fmt.Printf("- Pod: '%s' has been re-assigned to the same node. No move cost added.\n", pod.Name)
		} else {
			podMoveCost := Config.PodMoveCost //default value
			if annotationValue, ok := pod.Annotations[Config.PodMoveCostAnnotation]; ok {
				if customCost, err := strconv.Atoi(annotationValue); err == nil {
					podMoveCost = customCost
				}
			}
			fmt.Printf("- Pod '%s' has been moved to a different node. Added move cost of %d to the permutation total cost.\n", pod.Name, podMoveCost)
			permutationCost += podMoveCost
		}
	}

	// count the new free nodes number after scheduling the pods
	newEmptyNodesNum := 0
	for _, candidate := range candidateNodesToDrain {
		nodeName := candidate.Node().Name

		updatedNodeInfo, err := snapshot.NodeInfos().Get(nodeName)
		if err != nil {
			fmt.Printf("Warning: failed to get updated node %s from snapshot: %v", nodeName, err)
			continue
		}

		if isConsideredEmpty(updatedNodeInfo) {
			newEmptyNodesNum++
		}
	}
	freedNodesNum := newEmptyNodesNum - previousEmptyNodesNum
	fmt.Printf("This permutation freed %d nodes.\n", freedNodesNum)

	result := &SchedulingResult{
		permutation:   permutation,
		emptyNodesNum: freedNodesNum,
		cost:          permutationCost,
		score:         (Config.EmptyNodesScoreWeight * freedNodesNum) - (Config.CostScoreWeight * permutationCost),
	}

	snapshot.Revert()
	return result, nil
}

func isConsideredEmpty(node kubeframework.NodeInfo) bool {
	pods := node.GetPods()

	for _, pod := range pods {
		evictable, _ := isEvictable(pod.GetPod())
		if evictable {
			return false
		}
	}
	return true
}

func pickHighestScoreNode(nodes []kubeframework.NodeInfo, scores []kubeframework.NodePluginScores) (kubeframework.NodeInfo, error) {
	var maxScore int64 = 0
	var maxScoreNodeName string

	// pick the highest score
	for _, score := range scores {
		if score.TotalScore > maxScore {
			maxScore = score.TotalScore
			maxScoreNodeName = score.Name
		}
	}

	// retrieve the related nodeInfo
	for _, nodeInfo := range nodes {
		if nodeInfo.Node().Name == maxScoreNodeName {
			return nodeInfo, nil
		}
	}

	return nil, errors.New("The highest node found")
}
