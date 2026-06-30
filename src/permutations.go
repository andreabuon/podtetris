package main

import (
	"fmt"
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
	result := make([]*apiv1.Pod, len(pods))
	copy(result, pods)
	sort.Slice(result, func(i, j int) bool {
		return getPodCPURequests(result[i]) > getPodCPURequests(result[j])
	})
	return result
}

func sortByMemoryDesc(pods []*apiv1.Pod) []*apiv1.Pod {
	result := make([]*apiv1.Pod, len(pods))
	copy(result, pods)
	sort.Slice(result, func(i, j int) bool {
		return getPodMemoryRequests(result[i]) > getPodMemoryRequests(result[j])
	})
	return result
}

func shufflePods(pods []*apiv1.Pod) []*apiv1.Pod {
	result := make([]*apiv1.Pod, len(pods))
	copy(result, pods)
	rand.Shuffle(len(result), func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})
	return result
}

func generatePermutations(evictedPods []*apiv1.Pod, enabledStrategies []string) [][]*apiv1.Pod {
	var generatedPermutations [][]*apiv1.Pod

	for _, strategy := range enabledStrategies {
		strategyFn, ok := permutationStrategies[strategy]
		if !ok {
			fmt.Printf("Warning: unknown permutation strategy %q, skipping", strategy)
			continue
		}
		generatedPermutations = append(generatedPermutations, strategyFn(evictedPods))
	}

	return generatedPermutations
}
