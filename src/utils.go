package main

import (
	corev1 "k8s.io/api/core/v1"
)

const AnnotationFixed = "reply.com/podtetris/fixed"

// EvictionSkipReason describes why a pod was not evicted
type EvictionSkipReason string

const (
	SkipDaemonSet    EvictionSkipReason = "DaemonSet pod"
	SkipJob          EvictionSkipReason = "Job pod"
	SkipEmptyDir     EvictionSkipReason = "pod uses emptyDir volume"
	SkipStaticPod    EvictionSkipReason = "static pod"
	SkipLocalStorage EvictionSkipReason = "pod uses local storage"
	SkipFixed        EvictionSkipReason = "pod is fixed to the node"
)

// isEvictable returns (true, "") if the pod can be evicted or (false, reason) explaining why it was skipped.
func isEvictable(pod *corev1.Pod) (bool, EvictionSkipReason) {
	for _, owner := range pod.GetOwnerReferences() {
		switch owner.Kind {
		case "DaemonSet":
			return false, SkipDaemonSet
		case "Job":
			return false, SkipJob
		}
	}

	// Static pods (owned directly by a Node)
	for _, owner := range pod.GetOwnerReferences() {
		if owner.Kind == "Node" {
			return false, SkipStaticPod
		}
	}

	for _, vol := range pod.Spec.Volumes {
		if vol.EmptyDir != nil {
			return false, SkipEmptyDir
		}
		if vol.HostPath != nil {
			return false, SkipLocalStorage
		}
	}

	if val, ok := pod.Annotations[AnnotationFixed]; ok && val == "true" {
		return false, SkipFixed
	}

	return true, ""
}
