package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"

	"k8s.io/apimachinery/pkg/util/sets"
	kubeframework "k8s.io/kube-scheduler/framework"
)

func selectCandidateNodes(nodeInfos []kubeframework.NodeInfo, randomNodesToGet int, nodesToGetByCPU int, rules *RuleMatcher) ([]kubeframework.NodeInfo, error) {
	if nodeInfos == nil {
		return nil, errors.New("no available candidate nodes")
	}

	totalNodesToGet := randomNodesToGet + nodesToGetByCPU
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

	leastUsedNodes, err := getNodesByCPUUsage(allNodes.UnsortedList(), nodesToGetByCPU)
	if err != nil {
		return nil, err
	}

	remainingNodes := allNodes.Delete(leastUsedNodes...)

	var randomNodes []kubeframework.NodeInfo
	attemptNum := 0
	for {
		randomNodes, err = getRandomNodes(remainingNodes.UnsortedList(), randomNodesToGet)
		if err != nil {
			return nil, err
		}

		allContainFixed, err := allContainFixedPods(randomNodes, rules)
		if err != nil {
			return nil, fmt.Errorf("Error while checking candidate nodes: %v", err)
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
	candidateNodes = append(candidateNodes, leastUsedNodes...)
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

func isConsideredEmpty(node kubeframework.NodeInfo, rules *RuleMatcher) bool {
	pods := node.GetPods()

	for _, pod := range pods {
		evictable, _ := isEvictable(pod.GetPod(), rules)
		if evictable {
			return false
		}
	}
	return true
}

func countEmptyNodes(nodes []kubeframework.NodeInfo, rules *RuleMatcher) int {
	emptyNodes := 0

	for _, node := range nodes {
		if isConsideredEmpty(node, rules) {
			emptyNodes++
		}
	}
	return emptyNodes
}
