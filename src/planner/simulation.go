package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot"

	//"k8s.io/autoscaler/cluster-autoscaler/simulator/scheduling"
	kubeframework "k8s.io/kube-scheduler/framework"
	schedframework "k8s.io/kubernetes/pkg/scheduler/framework"
	//caframework "k8s.io/autoscaler/cluster-autoscaler/simulator/framework"
)

type SchedulingSimulator struct {
	framework schedframework.Framework
	snapshot  clustersnapshot.ClusterSnapshot
	baseline  *Baseline
}

type PodOrdering struct {
	Index int
	Pods  []*apiv1.Pod
}

type SimulationResult struct {
	Permutation *PodOrdering
	FreedNodes  int
	Cost        int
	Score       int
	Moves       []PodMove
}

type Baseline struct {
	// nodes considered for pods rescheduling
	CandidateNodes []kubeframework.NodeInfo
	// pods allocations before the rescheduling simulation
	Allocations map[types.NamespacedName]string
	// number of empty nodes before the rescheduling simulation
	EmptyNodeCount int
}

func virtuallyEvictPods(snapshot clustersnapshot.ClusterSnapshot, candidateNodes []kubeframework.NodeInfo) []*apiv1.Pod {
	var evictedPods []*apiv1.Pod

	for nodeIndex, nodeInfo := range candidateNodes {
		nodeName := nodeInfo.Node().Name
		var podsOnNode []*apiv1.Pod
		for _, podInfo := range nodeInfo.GetPods() {
			podsOnNode = append(podsOnNode, podInfo.GetPod())
		}

		log.Printf("[Candidate #%d] Node: %s", nodeIndex, nodeName)
		for _, pod := range podsOnNode {
			if ok, reason := isEvictable(pod); !ok {
				log.Printf("  > Skipping pod %s/%s: %s", pod.Namespace, pod.Name, reason)
				continue
			}

			if err := snapshot.UnschedulePod(pod.Namespace, pod.Name, nodeName); err != nil {
				log.Printf("Failed to virtually evict pod %s/%s: %v", pod.Namespace, pod.Name, err)
				continue
			}

			log.Println("  - Evicted pod:", pod.Name)

			unscheduledPod := pod.DeepCopy()
			unscheduledPod.Spec.NodeName = ""
			evictedPods = append(evictedPods, unscheduledPod)
		}
	}
	return evictedPods
}

func (s *SchedulingSimulator) Run(ctx context.Context, podsPermutation *PodOrdering) (*SimulationResult, error) {
	s.snapshot.Fork()
	defer s.snapshot.Revert()

	permutationCost := 0
	var moves []PodMove

	for _, pod := range podsPermutation.Pods {
		chosenNode, err := schedulePod(ctx, s.framework, s.snapshot, pod)
		if err != nil {
			return nil, err
		}

		// ForceAddPod is used instead of SchedulePod because the scheduler predicates have already been checked with RunFilterPlgins
		err = s.snapshot.ForceAddPod(pod, chosenNode.Node().Name)
		if err != nil {
			return nil, err
		}

		// Compute and display pod move cost
		podName := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}
		if chosenNode.Node().Name == s.baseline.Allocations[podName] {
			log.Printf("- Pod: '%s' has been re-assigned to the same node", pod.Name)
		} else {
			podMoveCost := getPodMoveCost(pod)
			permutationCost += podMoveCost
			pm := PodMove{
				pod:          pod,
				fromNodeName: s.baseline.Allocations[podName],
				toNodeName:   chosenNode.Node().Name,
				cost:         podMoveCost,
			}
			moves = append(moves, pm)
			log.Printf("- Pod move: %s", pm)
		}
	}

	// Re-fetch live NodeInfo data for each candidate node from the snapshot
	freshCandidateNodes := make([]kubeframework.NodeInfo, 0, len(s.baseline.CandidateNodes))
	for _, staleNode := range s.baseline.CandidateNodes {
		freshNode, err := s.snapshot.NodeInfos().Get(staleNode.Node().Name)
		if err != nil {
			return nil, fmt.Errorf("cannot retrieve fresh node info for %s: %w", staleNode.Node().Name, err)
		}
		freshCandidateNodes = append(freshCandidateNodes, freshNode)
	}

	newEmptyNodes := countEmptyNodes(freshCandidateNodes)
	freedNodes := newEmptyNodes - s.baseline.EmptyNodeCount

	result := &SimulationResult{
		Permutation: podsPermutation,
		FreedNodes:  freedNodes,
		Cost:        permutationCost,
		Score:       computePermutationScore(freedNodes, permutationCost),
		Moves:       moves,
	}

	return result, nil
}

func schedulePod(
	ctx context.Context,
	framework schedframework.Framework,
	snapshot clustersnapshot.ClusterSnapshot,
	pod *apiv1.Pod,
) (kubeframework.NodeInfo, error) {
	state := schedframework.NewCycleState()
	preFilterResult, preFilterStatus, _ := framework.RunPreFilterPlugins(ctx, state, pod)
	if !preFilterStatus.IsSuccess() {
		if preFilterStatus.Code() == kubeframework.Unschedulable {
			log.Printf("Pod %s/%s is unschedulable in this permutation: %v", pod.Namespace, pod.Name, preFilterStatus.Message())
			// Return a distinct error or handle it as a failed permutation path, not a system failure
			return nil, fmt.Errorf("pod unschedulable: %w", preFilterStatus.AsError())
		}

		return nil, fmt.Errorf("RunPreFilterPlugins failed: %v", preFilterStatus.AsError())
	}

	var preFilteredNodesNames []string
	if preFilterResult != nil {
		preFilteredNodesNames = preFilterResult.NodeNames.UnsortedList()
	} else {
		// No PreFilter plugin restricted the node set — consider all nodes.
		allNodeInfos, err := snapshot.NodeInfos().List()
		if err != nil {
			return nil, fmt.Errorf("cannot retrieve nodes after the PreFilter phase: %v", err)
		}
		for _, ni := range allNodeInfos {
			preFilteredNodesNames = append(preFilteredNodesNames, ni.Node().Name)
		}
	}

	var feasibleNodes []kubeframework.NodeInfo
	for _, preFilterNodeName := range preFilteredNodesNames {
		freshNodeInfo, err := snapshot.NodeInfos().Get(preFilterNodeName)
		if err != nil {
			return nil, fmt.Errorf("cannot retrieve a preFiltered node: %v", err)
		}

		filterStatus := framework.RunFilterPlugins(ctx, state, pod, freshNodeInfo)
		if filterStatus.IsSuccess() {
			feasibleNodes = append(feasibleNodes, freshNodeInfo)
		}
	}

	if len(feasibleNodes) == 0 {
		//The PostFilter stage is ignored
		return nil, fmt.Errorf("no feasible nodes have been found for pod %s", pod.Name)
	}

	preScoreStatus := framework.RunPreScorePlugins(ctx, state, pod, feasibleNodes)

	if !preScoreStatus.IsSuccess() {
		return nil, fmt.Errorf("PreScorePlugins failed: %v", preScoreStatus.AsError())
	}

	scores, status := framework.RunScorePlugins(ctx, state, pod, feasibleNodes)
	if !status.IsSuccess() {
		return nil, fmt.Errorf("ScorePlugins failed: %v", status.AsError())
	}

	bestNode, err := pickHighestScoreNode(feasibleNodes, scores)
	if err != nil {
		return nil, fmt.Errorf("pickHighestScoreNode failed: %v", err)
	}

	return bestNode, nil
}

func computePermutationScore(freedNodes, permutationCost int) int {
	return (Config.EmptyNodesScoreWeight * freedNodes) - (Config.CostScoreWeight * permutationCost)
}

func getPodMoveCost(pod *apiv1.Pod) int {
	podMoveCost := Config.PodMoveDefaultCost

	//TODO get Pod move cost by matching rules

	return podMoveCost
}

func pickHighestScoreNode(nodes []kubeframework.NodeInfo, scores []kubeframework.NodePluginScores) (kubeframework.NodeInfo, error) {
	if len(scores) == 0 {
		return nil, errors.New("no node scores available")
	}

	maxScore := scores[0].TotalScore
	maxScoreNodeName := scores[0].Name
	for _, score := range scores[1:] {
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

	return nil, errors.New("the node with the highest score cannot be found anymore")
}
