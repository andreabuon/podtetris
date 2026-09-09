package main

import (
	"context"
	"fmt"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// claimReplacement records that this PodMove's replacement CREATE has been intercepted
// Returns errPodMoveAlreadyClaimed if the move is no longer open.
// Returns a conflict error if the update races
func claimReplacement(ctx context.Context, pm *podtetrisiov1.PodMove, pod *corev1.Pod) error {
	if !isOpenForReplacement(pm) {
		return errPodMoveAlreadyClaimed
	}
	meta.SetStatusCondition(&pm.Status.Conditions, metav1.Condition{
		Type:               podtetrisiov1.ConditionReplacementClaimed,
		Status:             metav1.ConditionTrue,
		Reason:             conditionReasonReplacementCreated,
		Message:            fmt.Sprintf("Replacement pod %s/%s intercepted and pinned to node %q", pod.Namespace, podDisplayName(pod), pm.Spec.TargetNode),
		ObservedGeneration: pm.Generation,
	})
	return k8sClient.Status().Update(ctx, pm)
}

// isOpenForReplacement reports whether the PodMove is armed for a replacement CREATE.
func isOpenForReplacement(pm *podtetrisiov1.PodMove) bool {
	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionFailed) {
		return false
	}
	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionReplacementSucceeded) {
		return false
	}

	if meta.FindStatusCondition(pm.Status.Conditions, podtetrisiov1.ConditionSourceEvicted) == nil {
		return false
	}

	return meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionSourceEvicted) &&
		!meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionReplacementClaimed)
}

func replacementMatches(pm *podtetrisiov1.PodMove, pod *corev1.Pod, owner *metav1.OwnerReference) bool {
	if !ownerMatches(pm.Spec.Owner, *owner) {
		return false
	}
	if owner.Kind == "StatefulSet" {
		return pod.Name != "" && pod.Name == pm.Spec.Pod.Name
	}
	return true
}

func findMatchingPodMove(ctx context.Context, pod *corev1.Pod, skip map[string]struct{}) (*podtetrisiov1.PodMove, error) {
	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return nil, nil
	}

	var list podtetrisiov1.PodMoveList
	if err := k8sClient.List(ctx, &list, client.InNamespace(podtetrisNamespace)); err != nil {
		return nil, err
	}

	for i := range list.Items {
		pm := &list.Items[i]
		if !replacementMatches(pm, pod, owner) {
			continue
		}
		if _, skipped := skip[pm.Name]; skipped {
			continue
		}
		if !isOpenForReplacement(pm) {
			continue
		}
		if pm.Spec.TargetNode == "" {
			return nil, fmt.Errorf("PodMove %s/%s has empty spec.targetNode", pm.Namespace, pm.Name)
		}
		return pm, nil
	}
	return nil, nil
}
