package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	snapshot.Fork()

	permutationCost := 0

	for _, pod := range permutation {
		state := schedframework.NewCycleState()
		var feasibleNodes []kubeframework.NodeInfo

		preFilterResult, preFilterStatus, _ := realFramework.RunPreFilterPlugins(ctx, state, pod)
		if !preFilterStatus.IsSuccess() {
			log.Printf("Error: PreFilterPlugins failed")
			snapshot.Revert()
			return nil, errors.New("Error: PreFilterPlugins failed")
		}

		for _, preFilterNodeName := range preFilterResult.NodeNames.UnsortedList() {
			freshNodeInfo, err := snapshot.NodeInfos().Get(preFilterNodeName)
			if err != nil {
				return nil, errors.New("Error: can't retrieve a preFiltered node")
			}

			filterStatus := realFramework.RunFilterPlugins(ctx, state, pod, freshNodeInfo)
			if filterStatus.IsSuccess() {
				feasibleNodes = append(feasibleNodes, freshNodeInfo)
			}
		}

		if len(feasibleNodes) == 0 {
			//The PostFilter stage is ignored
			snapshot.Revert()
			return nil, fmt.Errorf("No feasible nodes have been found for pod %s", pod.Name)
		}

		preScoreStatus := realFramework.RunPreScorePlugins(ctx, state, pod, feasibleNodes)

		if !preScoreStatus.IsSuccess() {
			log.Printf("Error: PreScorePlugins failed")
			snapshot.Revert()
			return nil, errors.New("Error: PreScorePlugins failed")
		}

		scores, status := realFramework.RunScorePlugins(ctx, state, pod, feasibleNodes)
		if !status.IsSuccess() {
			log.Printf("Error: ScorePlugins failed")
			snapshot.Revert()
			return nil, errors.New("Error: ScorePlugins failed")
		}

		bestNode, err := pickHighestScoreNode(feasibleNodes, scores)
		if err != nil {
			log.Printf("Error: pickHighestScoreNode failed")
			snapshot.Revert()
			return nil, errors.New("Error: pickHighestScoreNode failed")
		}

		// ForceAddPod is used instead of SchedulPod because the scheduler predicates have already been checked with RunFilterPlgins
		snapshot.ForceAddPod(pod, bestNode.Node().Name)

		// Compute and display pod move cost
		podKey := pod.Namespace + "/" + pod.Name
		if bestNode.Node().Name == previousPodAllocations[podKey] {
			log.Printf("- Pod: '%s' has been re-assigned to the same node.", pod.Name)
		} else {
			podMoveCost := getPodMoveCost(pod)
			log.Printf("- Pod '%s' has been moved to node '%s'. Move cost %d.", pod.Name, bestNode.Node().Name, podMoveCost)
			permutationCost += podMoveCost
		}
	}

	newEmptyNodesNum := countEmptyNodes(candidateNodesToDrain)
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

func getPodMoveCost(pod *apiv1.Pod) int {
	podMoveCost := Config.PodMoveDefaultCost
	if annotationValue, ok := pod.Annotations[Config.PodMoveCostAnnotation]; ok {
		if customCost, err := strconv.Atoi(annotationValue); err == nil {
			podMoveCost = customCost
		}
	}
	return podMoveCost
}

func pickHighestScoreNode(nodes []kubeframework.NodeInfo, scores []kubeframework.NodePluginScores) (kubeframework.NodeInfo, error) {
	var maxScore int64 = 0
	var maxScoreNodeName string

	// pick the highest score
	for _, score := range scores {
		if score.TotalScore >= maxScore {
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

	return nil, errors.New("The node with the highest score cannot be found anymore")
}
