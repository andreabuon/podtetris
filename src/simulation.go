package main

import (
	"context"
	"errors"
	"log"
	"math/rand"
	"sort"
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

func selectCandidateNodes(nodeInfos []kubeframework.NodeInfo, randomNodesToGet int, nodesToGetByCPU int) ([]kubeframework.NodeInfo, error) {
	if nodeInfos == nil {
		return nil, errors.New("No available candidate nodes")
	}

	totalNodesToGet := randomNodesToGet + nodesToGetByCPU
	if len(nodeInfos) <= totalNodesToGet {
		return nil, errors.New("There are not enough candidate nodes")
	}

	if AllContainFixedPods(nodeInfos) {
		return nil, errors.New("Every node in the cluster contains fixed pods")
	}

	leastUsedNodes, err := getNodesByCPUUsage(nodeInfos, nodesToGetByCPU)
	if err != nil {
		return nil, err
	}

	var randomNodes []kubeframework.NodeInfo
	attemptNum := 0
	for {
		randomNodes, err = getRandomNodes(nodeInfos, randomNodesToGet)
		if err != nil {
			return nil, err
		}

		if !AllContainFixedPods(randomNodes) {
			break
		}

		attemptNum++
		if attemptNum >= Config.CandidateNodesSelectionMaxRetries {
			return nil, errors.New("Max random candidate nodes selection retries reached")
		}
	}

	var candidateNodes []kubeframework.NodeInfo
	candidateNodes = append(candidateNodes, leastUsedNodes...)
	candidateNodes = append(candidateNodes, randomNodes...)
	return candidateNodes, nil
}

func getNodesByCPUUsage(nodeInfos []kubeframework.NodeInfo, nodesNum int) ([]kubeframework.NodeInfo, error) {
	if nodeInfos == nil {
		return nil, errors.New("No available candidate nodes")
	}

	if len(nodeInfos) <= nodesNum {
		return nil, errors.New("There are not enough candidate nodes")
	}

	sort.Slice(
		nodeInfos,
		func(i, j int) bool {
			return nodeInfos[i].GetRequested().GetMilliCPU() < nodeInfos[j].GetRequested().GetMilliCPU()
		})

	var leastUsedNodes []kubeframework.NodeInfo
	for i := range nodesNum {
		leastUsedNodes = append(leastUsedNodes, nodeInfos[i])
	}
	return leastUsedNodes, nil
}

func getRandomNodes(nodeInfos []kubeframework.NodeInfo, nodesNum int) ([]kubeframework.NodeInfo, error) {
	if nodeInfos == nil {
		return nil, errors.New("No available candidate nodes")
	}

	if len(nodeInfos) <= nodesNum {
		return nil, errors.New("There are not enough candidate nodes")
	}

	var randomNodes []kubeframework.NodeInfo = make([]kubeframework.NodeInfo, nodesNum)
	randomIndices := rand.Perm(len(nodeInfos))
	for i := range nodesNum {
		randomIndex := randomIndices[i]
		randomNodes = append(randomNodes, nodeInfos[randomIndex])
	}
	return randomNodes, nil
}

func virtuallyEvictPods(snapshot clustersnapshot.ClusterSnapshot, candidateNodes []kubeframework.NodeInfo) []*apiv1.Pod {
	var evictedPods []*apiv1.Pod

	log.Print("Simulating the pods eviction from the candidate nodes in the snapshot...")
	for nodeIndex, nodeInfo := range candidateNodes {
		nodeName := nodeInfo.Node().Name
		var podsOnNode []*apiv1.Pod
		for _, podInfo := range nodeInfo.GetPods() {
			podsOnNode = append(podsOnNode, podInfo.GetPod())
		}

		log.Printf("[Candidate #%d] Node: %s", nodeIndex, nodeName)
		evictedPodsNum := 0
		for _, pod := range podsOnNode {
			if ok, reason := isEvictable(pod); !ok {
				log.Printf("  > Skipping pod %s/%s: %s", pod.Namespace, pod.Name, reason)
				continue
			}

			if err := snapshot.UnschedulePod(pod.Namespace, pod.Name, nodeName); err != nil {
				log.Printf("Warning: Failed to virtually evict pod %s/%s: %v", pod.Namespace, pod.Name, err)
				continue
			}

			log.Println("  - Evicted Pod:", pod.Name)
			evictedPodsNum++

			unscheduledPod := pod.DeepCopy()
			unscheduledPod.Spec.NodeName = ""
			evictedPods = append(evictedPods, unscheduledPod)
		}
		log.Printf("Evicted %d pods.", evictedPodsNum)
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
			log.Printf("Error during scheduling simulation: %v", err)
			snapshot.Revert()
			return nil, err
		}
	*/
	for _, pod := range permutation {
		state := schedframework.NewCycleState()
		//var newState kubeframework.CycleState = kubeframework

		var feasible []kubeframework.NodeInfo
		for _, nodeInfo := range candidateNodesToDrain {
			freshNodeInfo, err := snapshot.NodeInfos().Get(nodeInfo.Node().Name)
			if err != nil {
				continue
			}

			_, preFilterStatus, _ := realFramework.RunPreFilterPlugins(ctx, state, pod)
			if !preFilterStatus.IsSuccess() {
				continue
			}

			filterStatus := realFramework.RunFilterPlugins(ctx, state, pod, freshNodeInfo)
			if filterStatus.IsSuccess() {
				feasible = append(feasible, freshNodeInfo)
			}
		}

		if len(feasible) == 0 {
			// no node works for this pod
			//FIXME this should return an error
			continue
		}

		preScoreStatus := realFramework.RunPreScorePlugins(ctx, state, pod, feasible)

		if !preScoreStatus.IsSuccess() {
			log.Printf("Error: PreScorePlugins failed")
			snapshot.Revert()
			return nil, errors.New("Error: PreScorePlugins failed")
		}

		scores, status := realFramework.RunScorePlugins(ctx, state, pod, feasible)
		if !status.IsSuccess() {
			log.Printf("Error: ScorePlugins failed")
			snapshot.Revert()
			return nil, errors.New("Error: ScorePlugins failed")
		}

		bestNode, err := pickHighestScoreNode(feasible, scores)
		if err != nil {
			log.Printf("Error: pickHighestScoreNode failed")
			snapshot.Revert()
			return nil, errors.New("Error: pickHighestScoreNode failed")
		}

		snapshot.ForceAddPod(pod, bestNode.Node().Name)

		//log.Printf("Pod %s has been scheduled on node %s", pod.Name, bestNode.Node().Name)

		podKey := pod.Namespace + "/" + pod.Name
		if bestNode.Node().Name == previousPodAllocations[podKey] {
			log.Printf("- Pod: '%s' has been re-assigned to the same node.", pod.Name)
		} else {
			podMoveCost := Config.PodMoveDefaultCost //default value
			if annotationValue, ok := pod.Annotations[Config.PodMoveCostAnnotation]; ok {
				if customCost, err := strconv.Atoi(annotationValue); err == nil {
					podMoveCost = customCost
				}
			}
			log.Printf("- Pod '%s' has been moved to node '%s'. Move cost %d.", pod.Name, bestNode.Node().Name, podMoveCost)
			permutationCost += podMoveCost
		}
	}

	// count the new free nodes number after scheduling the pods
	newEmptyNodesNum := 0
	for _, candidate := range candidateNodesToDrain {
		nodeName := candidate.Node().Name

		updatedNodeInfo, err := snapshot.NodeInfos().Get(nodeName)
		if err != nil {
			log.Printf("Warning: failed to get updated node %s from snapshot: %v", nodeName, err)
			continue
		}

		if isConsideredEmpty(updatedNodeInfo) {
			newEmptyNodesNum++
		}
	}
	freedNodesNum := newEmptyNodesNum - previousEmptyNodesNum
	log.Printf("This permutation freed %d nodes.", freedNodesNum)

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
