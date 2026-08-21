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
	SkipSystemPods         EvictionSkipReason = "pod belongs to namespace 'kube-system'"
	SkipPodtetrisNamespace EvictionSkipReason = "pod belongs to podtetris own namespace"
)

// isEvictable returns (true, "") if the pod can be evicted or (false, reason) explaining why it was skipped.
func isEvictable(pod *apiv1.Pod) (bool, EvictionSkipReason) {
	if pod == nil {
		return false, SkipNilPod
	}

	if pod.Namespace == "kube-system" {
		return false, SkipSystemPods
	}

	if pod.Namespace == Config.PodtetrisNamespace {
		return false, SkipPodtetrisNamespace
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

func getControllerReference(pod *apiv1.Pod) *metav1.OwnerReference {
	return getControllerReferenceFromRefs(pod.OwnerReferences)
}

func getControllerReferenceFromRefs(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	return nil
}

func getPodCPURequests(pod *apiv1.Pod) (int64, error) {
	if pod == nil {
		return 0, errors.New("pod is nil")
	}

	var total int64 = 0
	for _, c := range pod.Spec.Containers {
		total += c.Resources.Requests.Cpu().Value()
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
