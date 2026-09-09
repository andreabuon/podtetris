package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// claimOpenPodMove lists matching open PodMoves once and tries to claim them in order.
// On already-claimed it skips to the next candidate.
// Status update conflicts (e.g. controller setting SourceEvicted=True) are retried on the same PodMove.
// Returns (nil, nil) when no open match is available.
func claimOpenPodMove(ctx context.Context, pod *corev1.Pod, dryRun bool) (*podtetrisiov1.PodMove, error) {
	candidates, err := listOpenPodMoveMatches(ctx, pod)
	if err != nil {
		return nil, fmt.Errorf("could not look up PodMove: %w", err)
	}

	for i := range candidates {
		pm := &candidates[i]
		if dryRun {
			log.Printf("Dry-run CREATE for pod %s/%s; skipping ReplacementClaimed update on PodMove %s/%s",
				pod.Namespace, podDisplayName(pod), pm.Namespace, pm.Name)
			return pm, nil
		}
		if err := claimReplacement(ctx, pm, pod); err != nil {
			if errors.Is(err, errPodMoveAlreadyClaimed) {
				log.Printf("PodMove %s/%s already claimed; trying next open move",
					pm.Namespace, pm.Name)
				continue
			}
			return nil, fmt.Errorf("could not mark PodMove replacement: %w", err)
		}
		return pm, nil
	}
	return nil, nil
}

func listOpenPodMoveMatches(ctx context.Context, pod *corev1.Pod) ([]podtetrisiov1.PodMove, error) {
	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return nil, nil
	}

	var list podtetrisiov1.PodMoveList
	if err := k8sClient.List(ctx, &list, client.InNamespace(podtetrisNamespace)); err != nil {
		return nil, err
	}

	out := make([]podtetrisiov1.PodMove, 0)
	for i := range list.Items {
		pm := &list.Items[i]
		if !replacementMatches(pm, pod, owner) || !isOpenForReplacement(pm) {
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
// Returns errPodMoveAlreadyClaimed if the move is no longer open.
// Retries on conflict so a concurrent controller status update (SourceEvicted) does not drop the claim.
func claimReplacement(ctx context.Context, pm *podtetrisiov1.PodMove, pod *corev1.Pod) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &podtetrisiov1.PodMove{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pm), current); err != nil {
			return err
		}
		if !isOpenForReplacement(current) {
			return errPodMoveAlreadyClaimed
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
		return nil
	})
}

// isOpenForReplacement reports whether the PodMove is armed for a replacement CREATE.
func isOpenForReplacement(pm *podtetrisiov1.PodMove) bool {
	conds := pm.Status.Conditions

	if meta.IsStatusConditionTrue(conds, podtetrisiov1.ConditionFailed) ||
		meta.IsStatusConditionTrue(conds, podtetrisiov1.ConditionReplacementSucceeded) {
		return false
	}

	if meta.FindStatusCondition(pm.Status.Conditions, podtetrisiov1.ConditionSourceEvicted) == nil {
		return false
	}

	return !meta.IsStatusConditionTrue(conds, podtetrisiov1.ConditionReplacementClaimed)
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
