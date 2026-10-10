package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"

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

// resolveCandidateNodesCounts turns the cluster-size-independent config into
// concrete per-strategy node counts for a cluster of clusterSize nodes.
// At least one node is always left out of the candidates so evicted pods have somewhere to go.
func resolveCandidateNodesCounts(clusterSize int, fraction float64, minNodes int, maxNodes int, mix CandidateNodesMixConfig) (CandidateNodesCounts, error) {
	if clusterSize < 2 {
		return CandidateNodesCounts{}, fmt.Errorf("at least 2 worker nodes are needed for consolidation, got %d", clusterSize)
	}

	total := int(math.Round(float64(clusterSize) * fraction))
	total = max(total, minNodes)
	total = min(total, maxNodes, clusterSize-1)

	weights := []float64{mix.ByCPU, mix.ByMemory, mix.Random}
	counts := apportion(total, weights)
	return CandidateNodesCounts{ByCPU: counts[0], ByMemory: counts[1], Random: counts[2]}, nil
}

// apportion splits total into integer parts proportional to weights using the
// largest remainder method, so the parts always sum to total.
func apportion(total int, weights []float64) []int {
	weightSum := 0.0
	for _, w := range weights {
		weightSum += w
	}

	counts := make([]int, len(weights))
	remainders := make([]float64, len(weights))
	assigned := 0
	for i, w := range weights {
		exact := float64(total) * w / weightSum
		counts[i] = int(math.Floor(exact))
		remainders[i] = exact - float64(counts[i])
		assigned += counts[i]
	}

	order := make([]int, len(weights))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return remainders[order[a]] > remainders[order[b]] })
	for i := 0; assigned < total; i++ {
		counts[order[i%len(order)]]++
		assigned++
	}
	return counts
}

func createCandidateNodesSets(nodeInfos []kubeframework.NodeInfo, setsToCreate int, counts CandidateNodesCounts, rules *RuleMatcher) ([]sets.Set[kubeframework.NodeInfo], error) {
	candidateSets := make([]sets.Set[kubeframework.NodeInfo], 0, setsToCreate)

	for len(candidateSets) < setsToCreate {
		nodes, err := selectCandidateNodes(nodeInfos, counts.Random, counts.ByCPU, counts.ByMemory, rules)
		if err != nil {
			return nil, err
		}
		candidateSets = append(candidateSets, sets.New(nodes...))
	}

	return candidateSets, nil
}

func selectCandidateNodes(nodeInfos []kubeframework.NodeInfo, randomNodesToGet int, nodesToGetByCPU int, nodesToGetByMemory int, rules *RuleMatcher) ([]kubeframework.NodeInfo, error) {
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

	var randomNodes []kubeframework.NodeInfo
	attemptNum := 0
	for {
		randomNodes, err = getRandomNodes(remainingNodes.UnsortedList(), randomNodesToGet)
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
