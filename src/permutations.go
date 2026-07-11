package main

import (
	"log"
	"math/rand"
	"sort"

	apiv1 "k8s.io/api/core/v1"
)

var permutationStrategies = map[string]func([]*apiv1.Pod) []*apiv1.Pod{
	"cpu_desc":    sortByCPUDesc,
	"memory_desc": sortByMemoryDesc,
	"random":      shufflePods,
}

func sortByCPUDesc(pods []*apiv1.Pod) []*apiv1.Pod {
	if pods == nil {
		return nil
	}

	requests := make(map[*apiv1.Pod]int64, len(pods))
	for _, p := range pods {
		req, err := getPodCPURequests(p)
		if err != nil {
			log.Printf("Warning: could not get CPU request for pod %s: %v", p.Name, err)
		}
		requests[p] = req
	}

	sort.SliceStable(pods, func(i, j int) bool {
		return requests[pods[i]] > requests[pods[j]]
	})

	return pods
}

func sortByMemoryDesc(pods []*apiv1.Pod) []*apiv1.Pod {
	if pods == nil {
		return nil
	}

	requests := make(map[*apiv1.Pod]int64, len(pods))
	for _, p := range pods {
		req, err := getPodMemoryRequests(p)
		if err != nil {
			log.Printf("Warning: could not get memory request for pod %s: %v", p.Name, err)
		}
		requests[p] = req
	}

	sort.SliceStable(pods, func(i, j int) bool {
		return requests[pods[i]] > requests[pods[j]]
	})

	return pods
}

func shufflePods(pods []*apiv1.Pod) []*apiv1.Pod {
	if pods == nil {
		return nil
	}

	rand.Shuffle(len(pods), func(i, j int) {
		pods[i], pods[j] = pods[j], pods[i]
	})

	return pods
}

func generatePermutations(evictedPods []*apiv1.Pod, enabledStrategies []string) [][]*apiv1.Pod {
	if evictedPods == nil {
		return nil
	}

	var generatedPermutations [][]*apiv1.Pod = make([][]*apiv1.Pod, 0, len(enabledStrategies))

	for _, strategy := range enabledStrategies {
		strategyFunc, ok := permutationStrategies[strategy]
		if !ok {
			log.Printf("Warning: unknown permutation strategy %q, skipping", strategy)
			continue
		}

		podsCopy := make([]*apiv1.Pod, len(evictedPods))
		copy(podsCopy, evictedPods)
		generatedPermutations = append(generatedPermutations, strategyFunc(podsCopy))
	}

	return generatedPermutations
}
