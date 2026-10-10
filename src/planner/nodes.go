package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/util/sets"
	kubeframework "k8s.io/kube-scheduler/framework"
)

type CandidateNodesCounts struct {
	ByCPU    int
	ByMemory int
	Random   int
}

func (c CandidateNodesCounts) Total() int {
	return c.ByCPU + c.ByMemory + c.Random
}

// resolveCandidateNodesCounts turns the configured percentages into node counts for a cluster of clusterSize nodes.
// The counts always sum to the total, and random always gets at least one node when its percentage is non-zero.
func resolveCandidateNodesCounts(clusterSize int, percent int, minNodes int, maxNodes int, mix CandidateNodesMixConfig) (CandidateNodesCounts, error) {
	if clusterSize < 2 {
		return CandidateNodesCounts{}, fmt.Errorf("at least 2 worker nodes are needed for consolidation, got %d", clusterSize)
	}

	// Integer division rounds down; adding 50 (half of 100) first rounds to the nearest node instead,
	// e.g. 30% of 22 nodes = 6.6 -> (660+50)/100 = 7 rather than 660/100 = 6.
	total := (clusterSize*percent + 50) / 100
	total = max(total, minNodes)
	// Every worker node can be a candidate: evicted pods may be rescheduled onto candidate nodes too.
	total = min(total, maxNodes, clusterSize)

	var counts CandidateNodesCounts
	// Random is rounded up (adding 99 before dividing), so any non-zero percentage gets at least one node,
	// e.g. 20% of 3 nodes = 0.6 -> 1.
	counts.Random = (total*mix.Random + 99) / 100
	// CPU is rounded to the nearest node (adding 50 before dividing), without taking more nodes than random left.
	counts.ByCPU = min((total*mix.ByCPU+50)/100, total-counts.Random)
	// Memory takes the rest, so the counts always sum to total.
	counts.ByMemory = total - counts.Random - counts.ByCPU
	return counts, nil
}

func createCandidateNodesSets(nodeInfos []kubeframework.NodeInfo, setsToCreate int, counts CandidateNodesCounts, rules *RuleMatcher) ([]sets.Set[kubeframework.NodeInfo], error) {
	candidateSets := make([]sets.Set[kubeframework.NodeInfo], 0, setsToCreate)
	// CPU/memory picks are deterministic, so sets stay distinct by not reusing random nodes.
	usedRandomNodes := sets.New[string]()

	for len(candidateSets) < setsToCreate {
		nodes, err := selectCandidateNodes(nodeInfos, counts.Random, counts.ByCPU, counts.ByMemory, rules, usedRandomNodes)
		if err != nil {
			if len(candidateSets) > 0 {
				log.Warn("Could not generate enough distinct candidate node sets",
					zap.Error(err),
					zap.Int("generated", len(candidateSets)),
					zap.Int("requested", setsToCreate),
				)
				break
			}
			return nil, err
		}
		candidateSets = append(candidateSets, sets.New(nodes...))
	}

	return candidateSets, nil
}

func selectCandidateNodes(nodeInfos []kubeframework.NodeInfo, randomNodesToGet int, nodesToGetByCPU int, nodesToGetByMemory int, rules *RuleMatcher, usedRandomNodes sets.Set[string]) ([]kubeframework.NodeInfo, error) {
	if nodeInfos == nil {
		return nil, errors.New("no available candidate nodes")
	}

	totalNodesToGet := randomNodesToGet + nodesToGetByCPU + nodesToGetByMemory
	if len(nodeInfos) < totalNodesToGet {
		return nil, errors.New("there are not enough candidate nodes")
	}

	allContainFixed, err := allContainFixedPods(nodeInfos, rules)
	if err != nil {
		return nil, fmt.Errorf("checking if all candidate nodes contain fixed pods failed: %v", err)
	}
	if allContainFixed {
		return nil, errors.New("every node in the cluster contains fixed pods")
	}

	allNodes := sets.New(nodeInfos...)

	leastUsedByCPU, err := getNodesByCPUUsage(allNodes.UnsortedList(), nodesToGetByCPU)
	if err != nil {
		return nil, err
	}
	remainingNodes := allNodes.Delete(leastUsedByCPU...)

	leastUsedByMemory, err := getNodesByMemoryUsage(remainingNodes.UnsortedList(), nodesToGetByMemory)
	if err != nil {
		return nil, err
	}
	remainingNodes = remainingNodes.Delete(leastUsedByMemory...)

	// Skip random nodes already used by earlier candidate sets.
	randomPool := make([]kubeframework.NodeInfo, 0, remainingNodes.Len())
	for _, node := range remainingNodes.UnsortedList() {
		if !usedRandomNodes.Has(node.Node().Name) {
			randomPool = append(randomPool, node)
		}
	}
	if len(randomPool) < randomNodesToGet {
		return nil, fmt.Errorf("not enough unused random candidate nodes: need %d, have %d", randomNodesToGet, len(randomPool))
	}

	var randomNodes []kubeframework.NodeInfo
	attemptNum := 0
	for {
		randomNodes, err = getRandomNodes(randomPool, randomNodesToGet)
		if err != nil {
			return nil, err
		}

		allContainFixed, err := allContainFixedPods(randomNodes, rules)
		if err != nil {
			return nil, fmt.Errorf("error while checking candidate nodes: %v", err)
		}
		if !allContainFixed {
			break
		}

		attemptNum++
		if attemptNum >= Config.CandidateNodesSelectionMaxRetries {
			return nil, errors.New("max random candidate nodes selection retries reached")
		}
	}

	for _, node := range randomNodes {
		usedRandomNodes.Insert(node.Node().Name)
	}

	var candidateNodes []kubeframework.NodeInfo
	candidateNodes = append(candidateNodes, leastUsedByCPU...)
	candidateNodes = append(candidateNodes, leastUsedByMemory...)
	candidateNodes = append(candidateNodes, randomNodes...)
	return candidateNodes, nil
}

func getNodesByCPUUsage(nodeInfos []kubeframework.NodeInfo, nodesNum int) ([]kubeframework.NodeInfo, error) {
	if nodeInfos == nil {
		return nil, errors.New("no available candidate nodes")
	}

	if len(nodeInfos) < nodesNum {
		return nil, errors.New("there are not enough candidate nodes")
	}

	sort.Slice(
		nodeInfos,
		func(i, j int) bool {
			return nodeInfos[i].GetRequested().GetMilliCPU() < nodeInfos[j].GetRequested().GetMilliCPU()
		})

	var leastUsedNodes []kubeframework.NodeInfo = make([]kubeframework.NodeInfo, nodesNum)
	for i := range nodesNum {
		leastUsedNodes[i] = nodeInfos[i]
	}
	return leastUsedNodes, nil
}

func getNodesByMemoryUsage(nodeInfos []kubeframework.NodeInfo, nodesNum int) ([]kubeframework.NodeInfo, error) {
	if nodeInfos == nil {
		return nil, errors.New("no available candidate nodes")
	}

	if len(nodeInfos) < nodesNum {
		return nil, errors.New("there are not enough candidate nodes")
	}

	sort.Slice(
		nodeInfos,
		func(i, j int) bool {
			return nodeInfos[i].GetRequested().GetMemory() < nodeInfos[j].GetRequested().GetMemory()
		})

	var leastUsedNodes []kubeframework.NodeInfo = make([]kubeframework.NodeInfo, nodesNum)
	for i := range nodesNum {
		leastUsedNodes[i] = nodeInfos[i]
	}
	return leastUsedNodes, nil
}

func getRandomNodes(nodeInfos []kubeframework.NodeInfo, nodesNum int) ([]kubeframework.NodeInfo, error) {
	if nodeInfos == nil {
		return nil, errors.New("no available candidate nodes")
	}

	if len(nodeInfos) < nodesNum {
		return nil, errors.New("there are not enough candidate nodes")
	}

	var randomNodes []kubeframework.NodeInfo = make([]kubeframework.NodeInfo, nodesNum)
	randomIndices := rand.Perm(len(nodeInfos))
	for i := range nodesNum {
		randomIndex := randomIndices[i]
		randomNodes[i] = nodeInfos[randomIndex]
	}
	return randomNodes, nil
}

func allContainFixedPods(nodeInfos []kubeframework.NodeInfo, rules *RuleMatcher) (bool, error) {
	if nodeInfos == nil {
		return false, errors.New("error while checking if all given nodes contain fixed pods: nodes are nil")
	}

	if len(nodeInfos) == 0 {
		return false, nil
	}

	for _, nodeInfo := range nodeInfos {
		foundFixedInCurrentNode := false
		for _, podInfo := range nodeInfo.GetPods() {
			pod := podInfo.GetPod()
			if pod == nil {
				continue
			}

			fixed, err := rules.isFixed(pod)
			if err != nil {

			}

			if fixed {
				foundFixedInCurrentNode = true
				break
			}
		}
		if !foundFixedInCurrentNode {
			return false, nil
		}
	}
	return true, nil
}

// isConsideredEmpty reports whether a node has no consolidatable workload left.
// Residual infra pods do not count;
// fixed pods and ordinary workloads do (the node is not empty if any remain).
func isConsideredEmpty(node kubeframework.NodeInfo, rules *RuleMatcher) bool {
	for _, podInfo := range node.GetPods() {
		if !isResidualPod(podInfo.GetPod(), rules) {
			return false
		}
	}
	return true
}

// getEmptyNodes returns the set of node names considered empty.
func getEmptyNodes(nodes []kubeframework.NodeInfo, rules *RuleMatcher) sets.Set[string] {
	empty := sets.New[string]()
	for _, node := range nodes {
		if isConsideredEmpty(node, rules) {
			empty.Insert(node.Node().Name)
		}
	}
	return empty
}
