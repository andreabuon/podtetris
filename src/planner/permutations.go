package main

import (
	"math/rand"
	"sort"

	"go.uber.org/zap"
	apiv1 "k8s.io/api/core/v1"
)

type permutationStrategy struct {
	apply func([]*apiv1.Pod)
	count int
}

func sortByCPUDesc(pods []*apiv1.Pod) {
	sortByRequestDesc(pods, getPodCPURequests, "CPU")
}

func sortByMemoryDesc(pods []*apiv1.Pod) {
	sortByRequestDesc(pods, getPodMemoryRequests, "memory")
}

func sortByRequestDesc(pods []*apiv1.Pod, requestOf func(*apiv1.Pod) (int64, error), resource string) {
	requests := make(map[*apiv1.Pod]int64, len(pods))
	for _, pod := range pods {
		req, err := requestOf(pod)
		if err != nil {
			log.Error("Could not get pod resource request",
				zap.Error(err),
				zap.String("resource", resource),
				zap.String("pod", pod.Name),
				zap.String("namespace", pod.Namespace),
			)
		}
		requests[pod] = req
	}

	sort.SliceStable(pods, func(i, j int) bool {
		return requests[pods[i]] > requests[pods[j]]
	})
}

func shufflePods(pods []*apiv1.Pod) {
	rand.Shuffle(len(pods), func(i, j int) {
		pods[i], pods[j] = pods[j], pods[i]
	})
}

func copyPods(pods []*apiv1.Pod) []*apiv1.Pod {
	copied := make([]*apiv1.Pod, len(pods))
	copy(copied, pods)
	return copied
}

func generatePermutations(evictedPods []*apiv1.Pod, enabledStrategies []string, randomCount int) [][]*apiv1.Pod {
	if evictedPods == nil {
		return nil
	}

	strategies := map[string]permutationStrategy{
		"cpu_desc":    {apply: sortByCPUDesc, count: 1},
		"memory_desc": {apply: sortByMemoryDesc, count: 1},
		"random":      {apply: shufflePods, count: randomCount},
	}

	var permutations [][]*apiv1.Pod
	for _, name := range enabledStrategies {
		strategy, ok := strategies[name]
		if !ok {
			log.Info("Unknown permutation strategy, skipping", zap.String("strategy", name))
			continue
		}

		for i := 0; i < strategy.count; i++ {
			pods := copyPods(evictedPods)
			strategy.apply(pods)
			permutations = append(permutations, pods)
		}
	}

	return permutations
}
