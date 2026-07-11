package main

import (
	"errors"

	apiv1 "k8s.io/api/core/v1"
	corev1 "k8s.io/api/core/v1"
)

// EvictionSkipReason describes why a pod was not evicted
type EvictionSkipReason string

const (
	SkipDaemonSet EvictionSkipReason = "DaemonSet pod"
	SkipJob       EvictionSkipReason = "Job pod"
	SkipStaticPod EvictionSkipReason = "static pod"
	SkipFixedPod  EvictionSkipReason = "pod is fixed to the node"
	SkipNilPod    EvictionSkipReason = "pod reference is nil"
)

// isEvictable returns (true, "") if the pod can be evicted or (false, reason) explaining why it was skipped.
func isEvictable(pod *corev1.Pod) (bool, EvictionSkipReason) {
	if pod == nil {
		return false, SkipNilPod
	}

	for _, owner := range pod.GetOwnerReferences() {
		switch owner.Kind {
		case "DaemonSet":
			return false, SkipDaemonSet
		case "Job":
			return false, SkipJob
		case "Node":
			return false, SkipStaticPod
		}
	}

	if value, ok := pod.Annotations[Config.FixedPodAnnotation]; ok && value == "true" {
		return false, SkipFixedPod
	}

	return true, ""
}

func getPodCPURequests(pod *apiv1.Pod) (int64, error) {
	if pod == nil {
		return 0, errors.New("Pod is nil")
	}

	var total int64 = 0
	for _, c := range pod.Spec.Containers {
		total += c.Resources.Requests.Cpu().Value()
	}
	return total, nil
}

func getPodMemoryRequests(pod *apiv1.Pod) (int64, error) {
	if pod == nil {
		return 0, errors.New("Pod is nil")
	}

	var total int64 = 0
	for _, c := range pod.Spec.Containers {
		total += c.Resources.Requests.Memory().Value()
	}
	return total, nil
}
