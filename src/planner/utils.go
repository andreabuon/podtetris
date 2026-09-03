package main

import (
	"errors"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EvictionSkipReason describes why a pod was not evicted
type EvictionSkipReason string

const (
	SkipDaemonSet          EvictionSkipReason = "daemonSet pod"
	SkipJob                EvictionSkipReason = "job pod"
	SkipStaticPod          EvictionSkipReason = "static pod"
	SkipFixedPod           EvictionSkipReason = "pod is fixed to the node"
	SkipNilPod             EvictionSkipReason = "pod reference is nil"
	SkipNoController       EvictionSkipReason = "no controller owner (would not be recreated after eviction)"
	SkipSystemPods         EvictionSkipReason = "pod belongs to namespace 'kube-system'"
	SkipPodtetrisNamespace EvictionSkipReason = "pod belongs to podtetris own namespace"
)

// isEvictable returns (true, "") if the pod can be evicted or (false, reason) explaining why it was skipped.
func isEvictable(pod *apiv1.Pod, rules *RuleMatcher) (bool, EvictionSkipReason) {
	if pod == nil {
		return false, SkipNilPod
	}

	if pod.Namespace == "kube-system" {
		return false, SkipSystemPods
	}

	if pod.Namespace == Config.PodtetrisNamespace {
		return false, SkipPodtetrisNamespace
	}

	// Bare pods (and any pods without a controller) would not be recreated after eviction
	if metav1.GetControllerOf(pod) == nil {
		return false, SkipNoController
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

	fixed, err := rules.isFixed(pod)
	if err != nil {
		//FIXME
	}
	if fixed {
		return false, SkipFixedPod
	}

	return true, ""
}

func getPodCPURequests(pod *apiv1.Pod) (int64, error) {
	if pod == nil {
		return 0, errors.New("pod is nil")
	}

	var total int64 = 0
	for _, c := range pod.Spec.Containers {
		total += c.Resources.Requests.Cpu().MilliValue()
	}
	return total, nil
}

func getPodMemoryRequests(pod *apiv1.Pod) (int64, error) {
	if pod == nil {
		return 0, errors.New("pod is nil")
	}

	var total int64 = 0
	for _, c := range pod.Spec.Containers {
		total += c.Resources.Requests.Memory().Value()
	}
	return total, nil
}
