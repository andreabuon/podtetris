package main

import (
	"context"
	"fmt"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func listOpenPodMoveMatches(ctx context.Context, pod *corev1.Pod) ([]podtetrisiov1.PodMove, error) {
	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return nil, fmt.Errorf("no owner controller found for the pod")
	}

	var list podtetrisiov1.PodMoveList
	if err := k8sClient.List(ctx, &list, client.InNamespace(podtetrisNamespace)); err != nil {
		return nil, err
	}

	out := make([]podtetrisiov1.PodMove, 0)
	for i := range list.Items {
		pm := &list.Items[i]
		if !replacementMatches(pm, pod, owner) {
			continue
		}
		if !isOpenForReplacement(pm) {
			continue
		}
		if pm.Spec.TargetNode == "" {
			return nil, fmt.Errorf("PodMove %s/%s has empty spec.targetNode", pm.Namespace, pm.Name)
		}
		out = append(out, *pm)
	}
	return out, nil
}

// claimReplacement records that this PodMove's replacement CREATE has been intercepted.
// claimed is false when the move closed between list and update (caller should try the next candidate).
// Retries on conflict so a concurrent controller status update does not drop the claim.
func claimReplacement(ctx context.Context, pm *podtetrisiov1.PodMove, pod *corev1.Pod) (claimed bool, err error) {
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		claimed = false
		current := &podtetrisiov1.PodMove{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pm), current); err != nil {
			return err
		}
		if !isOpenForReplacement(current) {
			return nil
		}
		meta.SetStatusCondition(&current.Status.Conditions, metav1.Condition{
			Type:               podtetrisiov1.ConditionReplacementClaimed,
			Status:             metav1.ConditionTrue,
			Reason:             conditionReasonReplacementCreated,
			Message:            fmt.Sprintf("Replacement pod %s/%s intercepted and pinned to node %q", pod.Namespace, podDisplayName(pod), current.Spec.TargetNode),
			ObservedGeneration: current.Generation,
		})
		if err := k8sClient.Status().Update(ctx, current); err != nil {
			return err
		}
		*pm = *current
		claimed = true
		return nil
	})
	return claimed, err
}

// isOpenForReplacement reports whether the PodMove is armed for a replacement CREATE.
func isOpenForReplacement(pm *podtetrisiov1.PodMove) bool {
	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionFailed) ||
		meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionReplacementSucceeded) ||
		meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionReplacementClaimed) {
		return false
	}
	return meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionSourceEvicting)
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
