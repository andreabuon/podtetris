package main

import (
	"errors"
	"math/rand"
	"sort"

	kubeframework "k8s.io/kube-scheduler/framework"
)

func selectCandidateNodes(nodeInfos []kubeframework.NodeInfo, randomNodesToGet int, nodesToGetByCPU int) ([]kubeframework.NodeInfo, error) {
	if nodeInfos == nil {
		return nil, errors.New("No available candidate nodes")
	}

	totalNodesToGet := randomNodesToGet + nodesToGetByCPU
	if len(nodeInfos) <= totalNodesToGet {
		return nil, errors.New("There are not enough candidate nodes")
	}

	if allContainFixedPods(nodeInfos) {
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

		if !allContainFixedPods(randomNodes) {
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

func allContainFixedPods(nodeInfos []kubeframework.NodeInfo) bool {
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
